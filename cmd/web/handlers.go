package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/snirkop89/go-payments/internal/cards"
	"github.com/snirkop89/go-payments/internal/encryption"
	"github.com/snirkop89/go-payments/internal/models"
	"github.com/snirkop89/go-payments/internal/urlsigner"
)

func (app *application) Home(w http.ResponseWriter, r *http.Request) {

	if err := app.renderTemplate(w, r, "home", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) VirtualTerminal(w http.ResponseWriter, r *http.Request) {

	if err := app.renderTemplate(w, r, "terminal", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}

type TransactionData struct {
	FirstName       string
	LastName        string
	Email           string
	PaymentIntentID string
	PaymentMethodID string
	PaymentAmount   int
	PaymentCurrency string
	LastFour        string
	ExpiryMonth     int
	ExpiryYear      int
	BankReturnCode  string
}

// getTransactionData populates transaction data from post request and stripe
func (app *application) getTransactionData(r *http.Request) (TransactionData, error) {
	err := r.ParseForm()
	if err != nil {
		app.errorLog.Println(err)
		return TransactionData{}, err
	}

	// read posted data
	firstName := r.Form.Get("first_name")
	lastName := r.Form.Get("last_name")
	email := r.Form.Get("email")
	paymentIntent := r.Form.Get("payment_intent")
	paymentMethod := r.Form.Get("payment_method")
	paymentAmount := r.Form.Get("payment_amount")
	paymentCurrency := r.Form.Get("payment_currency")
	// create a new transaction
	amount, err := strconv.Atoi(paymentAmount)
	if err != nil {
		app.errorLog.Println(err)
		return TransactionData{}, err
	}

	card := cards.Card{
		Secret: app.config.stripe.secret,
		Key:    app.config.stripe.key,
	}

	pi, err := card.RetrievePaymentIntent(paymentIntent)
	if err != nil {
		app.errorLog.Println(err)
		return TransactionData{}, err
	}

	pm, err := card.GetPaymentMethod(paymentMethod)
	if err != nil {
		app.errorLog.Println(err)
		return TransactionData{}, err
	}

	lastFour := pm.Card.Last4
	expiryMonth := pm.Card.ExpMonth
	expiryYear := pm.Card.ExpYear

	txnData := TransactionData{
		FirstName:       firstName,
		LastName:        lastName,
		Email:           email,
		PaymentIntentID: paymentIntent,
		PaymentMethodID: paymentMethod,
		PaymentAmount:   amount,
		PaymentCurrency: paymentCurrency,
		LastFour:        lastFour,
		ExpiryMonth:     int(expiryMonth),
		ExpiryYear:      int(expiryYear),
		BankReturnCode:  pi.Charges.Data[0].ID,
	}

	return txnData, nil
}

type Invoice struct {
	ID        int       `json:"id"`
	Quantity  int       `json:"quantity"`
	Amount    int       `json:"amount"`
	Product   string    `json:"product"`
	FirstName string    `json:"first_name"`
	LastName  string    `json:"last_name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

func (app *application) PaymentSucceeded(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	widgetID, _ := strconv.Atoi(r.Form.Get("product_id"))

	txnData, err := app.getTransactionData(r)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	// create a new customer
	custID, err := app.SaveCustomer(txnData.FirstName, txnData.LastName, txnData.Email)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	app.infoLog.Println(custID)

	txn := models.Transaction{
		Amount:              txnData.PaymentAmount,
		Currency:            txnData.PaymentCurrency,
		LastFour:            txnData.LastFour,
		ExpiryMonth:         txnData.ExpiryMonth,
		ExpiryYear:          txnData.ExpiryYear,
		BankReturnCode:      txnData.BankReturnCode,
		TransactionStatusID: models.TransactionCleared,
		PaymentIntent:       txnData.PaymentIntentID,
		PaymentMethod:       txnData.PaymentMethodID,
	}

	txnID, err := app.SaveTransaction(txn)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	// create a new order
	order := models.Order{
		WidgetID:      widgetID,
		TransactionID: txnID,
		CustomerID:    custID,
		StatusID:      models.StatusCleared,
		Quantity:      1,
		Amount:        txnData.PaymentAmount,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	orderID, err := app.SaveOrder(order)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	// call the invoice microservice
	invoice := Invoice{
		ID:        orderID,
		Quantity:  order.Quantity,
		Amount:    order.Amount,
		Product:   "Widget",
		FirstName: txnData.FirstName,
		LastName:  txnData.LastName,
		Email:     txnData.Email,
		CreatedAt: time.Now(),
	}

	err = app.callInvoiceMicro(invoice)
	if err != nil {
		app.errorLog.Println(err)
	}

	// write this data to a sessions, and then redirect to new page
	app.session.Put(r.Context(), "receipt", txnData)

	http.Redirect(w, r, "/receipt", http.StatusSeeOther)
}

func (app *application) callInvoiceMicro(inv Invoice) error {
	url := "http://localhost:5000/invoice/create-and-send"
	out, err := json.MarshalIndent(inv, "", "\t")
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(out))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	app.infoLog.Println(resp.Body)

	return nil
}

// VirtualTerminalPaymentSucceeded displayed the receipt page for virutal terminal transactions
func (app *application) VirtualTerminalPaymentSucceeded(w http.ResponseWriter, r *http.Request) {
	txnData, err := app.getTransactionData(r)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	txn := models.Transaction{
		Amount:              txnData.PaymentAmount,
		Currency:            txnData.PaymentCurrency,
		LastFour:            txnData.LastFour,
		ExpiryMonth:         txnData.ExpiryMonth,
		ExpiryYear:          txnData.ExpiryYear,
		BankReturnCode:      txnData.BankReturnCode,
		TransactionStatusID: models.TransactionCleared,
		PaymentIntent:       txnData.PaymentIntentID,
		PaymentMethod:       txnData.PaymentMethodID,
	}

	_, err = app.SaveTransaction(txn)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	// write this data to a sessions, and then redirect to new page
	app.session.Put(r.Context(), "receipt", txnData)

	http.Redirect(w, r, "/virtual-terminal-receipt", http.StatusSeeOther)
}

func (app *application) Receipt(w http.ResponseWriter, r *http.Request) {
	txnData, ok := app.session.Get(r.Context(), "receipt").(TransactionData)
	if !ok {
		http.Error(w, "data removed for privacy protection", http.StatusForbidden)
		return
	}
	app.session.Remove(r.Context(), "receipt")

	data := make(map[string]any)
	data["txn"] = txnData

	if err := app.renderTemplate(w, r, "receipt", &templateData{
		Data: data,
	}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) VirtualTerminalReceipt(w http.ResponseWriter, r *http.Request) {
	txnData, ok := app.session.Get(r.Context(), "receipt").(TransactionData)
	if !ok {
		http.Error(w, "data removed for privacy protection", http.StatusForbidden)
		return
	}
	app.session.Remove(r.Context(), "receipt")

	data := make(map[string]any)
	data["txn"] = txnData

	if err := app.renderTemplate(w, r, "virtual-terminal-receipt", &templateData{
		Data: data,
	}); err != nil {
		app.errorLog.Println(err)
	}
}

// SaveCustomer saves a customer and returns id
func (app *application) SaveCustomer(firstName, lastName, email string) (int, error) {
	customer := models.Customer{
		FirstName: firstName,
		LastName:  lastName,
		Email:     email,
	}

	id, err := app.DB.InsertCustomer(customer)
	if err != nil {
		return 0, err
	}

	return id, err
}

// SaveTransaction saves a transaction in the db and returns id
func (app *application) SaveTransaction(txn models.Transaction) (int, error) {
	id, err := app.DB.InsertTransaction(txn)
	if err != nil {
		return 0, err
	}

	return id, err
}

// SaveOrder saves an order in the db and returns id
func (app *application) SaveOrder(order models.Order) (int, error) {
	id, err := app.DB.InsertOrder(order)
	if err != nil {
		return 0, err
	}

	return id, err
}

// ChargeOnce displays the page to buy one widget
func (app *application) ChargeOnce(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	widgetID, err := strconv.Atoi(id)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	widget, err := app.DB.GetWidget(widgetID)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	data := make(map[string]any)
	data["widget"] = widget

	if err := app.renderTemplate(w, r, "buy-once", &templateData{Data: data}, "stripe-js"); err != nil {
		app.errorLog.Println(err)
	}
}

// BronzePlan displays the page to subsribe into a monthly plan
func (app *application) BronzePlan(w http.ResponseWriter, r *http.Request) {
	widget, err := app.DB.GetWidget(2)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	data := make(map[string]any)
	data["widget"] = widget

	if err := app.renderTemplate(w, r, "bronze-plan", &templateData{
		Data: data,
	}); err != nil {
		app.errorLog.Println(err)
	}
}

// BronzePlanReceipt displays the receipt of a successful purchase
func (app *application) BronzePlanReceipt(w http.ResponseWriter, r *http.Request) {
	if err := app.renderTemplate(w, r, "receipt-plan", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) LoginPage(w http.ResponseWriter, r *http.Request) {
	if err := app.renderTemplate(w, r, "login", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) PostLoginPage(w http.ResponseWriter, r *http.Request) {
	app.session.RenewToken(r.Context())

	err := r.ParseForm()
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	email := r.Form.Get("email")
	password := r.Form.Get("password")

	id, err := app.DB.Authenticate(email, password)
	if err != nil {
		app.errorLog.Println(err)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	app.session.Put(r.Context(), "userID", id)
	http.Redirect(w, r, "/", http.StatusOK)
}

func (app *application) Logout(w http.ResponseWriter, r *http.Request) {
	app.session.Destroy(r.Context())
	app.session.RenewToken(r.Context())

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (app *application) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if err := app.renderTemplate(w, r, "forgot-password", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) ShowResetPassword(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	theURL := r.RequestURI
	testURL := fmt.Sprintf("%s%s", app.config.frontend, theURL)

	signer := urlsigner.Signer{
		Secret: []byte(app.config.secretKey),
	}

	valid := signer.VerifyToken(testURL)

	if !valid {
		app.errorLog.Println("Invalid url - tampering detected")
		return
	}

	// make sure not expired
	expired := signer.Expired(testURL, 60)
	if expired {
		app.errorLog.Println("Link expired")
		return
	}

	encryptor := encryption.Encryption{
		Key: []byte(app.config.secretKey),
	}

	encryptedEmail, err := encryptor.Encrypt(email)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	data := make(map[string]any)
	data["email"] = encryptedEmail

	if err := app.renderTemplate(w, r, "reset-password", &templateData{
		Data: data,
	}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) AllSales(w http.ResponseWriter, r *http.Request) {
	if err := app.renderTemplate(w, r, "all-sales", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) AllSubscriptions(w http.ResponseWriter, r *http.Request) {
	if err := app.renderTemplate(w, r, "all-subscriptions", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) ShowSale(w http.ResponseWriter, r *http.Request) {
	stringMap := make(map[string]string)
	stringMap["title"] = "Sale"
	stringMap["cancel"] = "/admin/all-sales"
	stringMap["refund-url"] = "/api/admin/refund"
	stringMap["refund-btn"] = "Refund Order"
	stringMap["success-message"] = "Charge Refunded"
	stringMap["refund-badge"] = "Refunded"

	if err := app.renderTemplate(w, r, "sale", &templateData{
		StringMap: stringMap,
	}); err != nil {
		app.errorLog.Println(err)
	}
}

func (app *application) ShowSubscription(w http.ResponseWriter, r *http.Request) {
	stringMap := make(map[string]string)
	stringMap["title"] = "Subscription"
	stringMap["cancel"] = "/admin/all-subscriptions"
	stringMap["refund-url"] = "/api/admin/cancel-subscription"
	stringMap["refund-btn"] = "Cancel Subscription"
	stringMap["success-message"] = "Subscription Cancelled"
	stringMap["refund-badge"] = "Cancelled"

	if err := app.renderTemplate(w, r, "sale", &templateData{
		StringMap: stringMap,
	}); err != nil {
		app.errorLog.Println(err)
	}
}

// AllUsers shows the all users page
func (app *application) AllUsers(w http.ResponseWriter, r *http.Request) {
	if err := app.renderTemplate(w, r, "all-users", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}

// OneUser shows one admin user for add/edit/delete
func (app *application) OneUser(w http.ResponseWriter, r *http.Request) {
	if err := app.renderTemplate(w, r, "one-user", &templateData{}); err != nil {
		app.errorLog.Println(err)
	}
}
