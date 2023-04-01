package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/snirkop89/go-payments/internal/cards"
	"github.com/snirkop89/go-payments/internal/encryption"
	"github.com/snirkop89/go-payments/internal/models"
	"github.com/snirkop89/go-payments/internal/urlsigner"
	"github.com/snirkop89/go-payments/internal/validator"
	"github.com/stripe/stripe-go/v72"
	"golang.org/x/crypto/bcrypt"
)

type stripePayload struct {
	Currency      string `json:"currency"`
	Amount        string `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	Email         string `json:"email"`
	CardBrand     string `json:"card_brand"`
	ExpiryMonth   int    `json:"exp_month"`
	ExpiryYear    int    `json:"exp_year"`
	LastFour      string `json:"last_four"`
	Plan          string `json:"plan"`
	ProductID     string `json:"product_id"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
}

type jsonReponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Content string `json:"content,omitempty"`
	ID      int    `json:"id,omitempty"`
}

func (app *application) GetPaymentIntent(w http.ResponseWriter, r *http.Request) {
	var payload stripePayload

	err := json.NewDecoder(r.Body).Decode(&payload)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	amount, err := strconv.Atoi(payload.Amount)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	card := cards.Card{
		Secret:   app.config.stripe.secret,
		Key:      app.config.stripe.key,
		Currency: payload.Currency,
	}

	okay := true
	pi, msg, err := card.Charge(payload.Currency, amount)
	if err != nil {
		okay = false
	}

	if okay {
		out, err := json.MarshalIndent(pi, "", "  ")
		if err != nil {
			app.errorLog.Println(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	} else {
		j := jsonReponse{
			OK:      false,
			Message: msg,
			Content: "",
		}

		out, err := json.MarshalIndent(j, "", "  ")
		if err != nil {
			app.errorLog.Println(err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(out)
	}

}

func (app *application) GetWidgetByID(w http.ResponseWriter, r *http.Request) {
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

	out, err := json.MarshalIndent(widget, "", "  ")
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
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

func (app *application) CreateCustomerAndSubToPlan(w http.ResponseWriter, r *http.Request) {
	var data stripePayload

	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		app.errorLog.Println(err)
		return
	}

	// validate data
	v := validator.New()
	v.Check(len(data.FirstName) > 1, "first_name", "first name must be longer than 2 characters")
	v.Check(len(data.LastName) > 1, "last_name", "last name must be longer than 2 characters")

	if !v.Valid() {
		app.failedValidation(w, r, v.Errors)
		return
	}

	card := cards.Card{
		Secret:   app.config.stripe.secret,
		Key:      app.config.stripe.key,
		Currency: data.Currency,
	}

	okay := true
	var subscription *stripe.Subscription
	txnMsg := "Transaction successful"

	stripeCustomer, msg, err := card.CreateCustomer(data.PaymentMethod, data.Email)
	if err != nil {
		app.errorLog.Println(err)
		okay = false
		txnMsg = msg
	}

	if okay {
		subscription, err = card.SubscribeToPlan(stripeCustomer, data.Plan, data.Email, data.LastFour, "")
		if err != nil {
			app.errorLog.Println(err)
			okay = false
			txnMsg = "Error subscribing customer"
		}

		app.infoLog.Println("subscription id is", subscription.ID)
	}
	if okay {
		productID, _ := strconv.Atoi(data.ProductID)
		customerID, err := app.SaveCustomer(data.FirstName, data.LastName, data.Email)
		if err != nil {
			app.errorLog.Println(err)
			return
		}

		// create a new transaction
		amount, err := strconv.Atoi(data.Amount)
		if err != nil {
			app.errorLog.Println(err)
			return
		}

		txn := models.Transaction{
			Amount:              amount,
			Currency:            "usd",
			LastFour:            data.LastFour,
			ExpiryMonth:         data.ExpiryMonth,
			ExpiryYear:          data.ExpiryYear,
			TransactionStatusID: models.TransactionCleared,
			PaymentIntent:       subscription.ID,
			PaymentMethod:       data.PaymentMethod,
		}

		txnID, err := app.SaveTransaction(txn)
		if err != nil {
			app.errorLog.Println(err)
			return
		}

		// create order
		order := models.Order{
			WidgetID:      productID,
			TransactionID: txnID,
			CustomerID:    customerID,
			StatusID:      models.StatusCleared,
			Quantity:      1,
			Amount:        amount,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		invoice := Invoice{
			ID:        productID,
			Quantity:  order.Quantity,
			Amount:    2000,
			Product:   "Bronze Plan mongthly subscription",
			FirstName: data.FirstName,
			LastName:  data.LastName,
			Email:     data.Email,
			CreatedAt: time.Now(),
		}

		err = app.callInvoiceMicro(invoice)
		if err != nil {
			app.errorLog.Println(err)
		}

		_, err = app.SaveOrder(order)
		if err != nil {
			app.errorLog.Println(err)
			return
		}

	}

	resp := jsonReponse{
		OK:      okay,
		Message: txnMsg,
	}

	out, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		app.errorLog.Println(err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
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

	return nil
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

func (app *application) CreateAuthToken(w http.ResponseWriter, r *http.Request) {
	var userInput struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	err := app.readJSON(w, r, &userInput)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	// get the user from the database by email; send error if invliad email
	user, err := app.DB.GetUserByEmail(userInput.Email)
	if err != nil {
		app.invalidCredentials(w)
		return
	}

	// validate the password; send error if invalid password
	validPassword, err := app.passwordMatches(user.Password, userInput.Password)
	if err != nil {
		app.invalidCredentials(w)
		return
	}

	if !validPassword {
		app.invalidCredentials(w)
		return
	}

	// generate the token
	token, err := models.GenerateToken(user.ID, 24*time.Hour, models.ScopeAuthentication)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	// save to database
	err = app.DB.InsertToken(token, user)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	// send response
	var payload = struct {
		Error   bool          `json:"error"`
		Message string        `json:"message"`
		Token   *models.Token `json:"authentication_token"`
	}{
		Error:   false,
		Message: fmt.Sprintf("token for %s created", userInput.Email),
		Token:   token,
	}

	err = app.writeJSON(w, http.StatusOK, payload)
	if err != nil {
		if err != nil {
			// TODO: change to internal server
			app.badRequest(w, r, err)
			return
		}
	}
}

func (app *application) authenticateToken(r *http.Request) (*models.User, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, errors.New("no authorization header received")
	}

	headerParts := strings.Fields(authHeader)
	if len(headerParts) != 2 || headerParts[0] != "Bearer" {
		return nil, errors.New("no authorization header received")
	}

	token := headerParts[1]

	if len(token) != 26 {
		return nil, errors.New("authentication token wrong size")
	}

	// get the user from the tokens table
	user, err := app.DB.GetUserForToken(token)
	if err != nil {
		return nil, errors.New("no matching user found")
	}

	return user, nil
}

func (app *application) CheckAuthentication(w http.ResponseWriter, r *http.Request) {
	// validate the token, and get associated user
	user, err := app.authenticateToken(r)
	if err != nil {
		app.errorLog.Println(err)
		app.invalidCredentials(w)
		return
	}

	// valid user
	var payload struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}
	payload.Error = false
	payload.Message = fmt.Sprintf("authenticated user %s", user.Email)
	app.writeJSON(w, http.StatusOK, payload)
}

func (app *application) virtualTerminalPaymentSucceeded(w http.ResponseWriter, r *http.Request) {
	var txnData struct {
		PaymentAmount   int    `json:"amount"`
		PaymentCurrency string `json:"currency"`
		FirstName       string `json:"first_name"`
		LastName        string `json:"last_name"`
		Email           string `json:"email"`
		PaymentIntent   string `json:"payment_intent"`
		PaymentMethod   string `json:"payment_method"`
		BankReturnCode  string `json:"bank_return_code"`
		ExpiryMonth     int    `json:"expiry_month"`
		ExpiryYear      int    `json:"expiry_year"`
		LastFour        string `json:"last_four"`
	}

	err := app.readJSON(w, r, &txnData)
	if err != nil {
		app.errorLog.Println(err)
		app.badRequest(w, r, err)
		return
	}

	card := cards.Card{
		Secret: app.config.stripe.secret,
		Key:    app.config.stripe.key,
	}

	pi, err := card.RetrievePaymentIntent(txnData.PaymentIntent)
	if err != nil {
		app.errorLog.Println(err)
		app.badRequest(w, r, err)
		return
	}

	pm, err := card.GetPaymentMethod(txnData.PaymentMethod)
	if err != nil {
		app.errorLog.Println(err)
		app.badRequest(w, r, err)
		return
	}

	txnData.LastFour = pm.Card.Last4
	txnData.ExpiryMonth = int(pm.Card.ExpMonth)
	txnData.ExpiryYear = int(pm.Card.ExpYear)

	txn := models.Transaction{
		Amount:              txnData.PaymentAmount,
		Currency:            txnData.PaymentCurrency,
		LastFour:            txnData.LastFour,
		ExpiryMonth:         txnData.ExpiryMonth,
		ExpiryYear:          txnData.ExpiryYear,
		BankReturnCode:      pi.Charges.Data[0].ID,
		TransactionStatusID: models.TransactionCleared,
		PaymentIntent:       txnData.PaymentIntent,
		PaymentMethod:       txnData.PaymentMethod,
	}

	_, err = app.SaveTransaction(txn)
	if err != nil {
		app.errorLog.Println(err)
		app.badRequest(w, r, err)
		return
	}

	app.writeJSON(w, http.StatusOK, txn)
}

func (app *application) SendPasswordResetEmail(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Email string `json:"email"`
	}

	err := app.readJSON(w, r, &payload)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	// verify that email exist
	_, err = app.DB.GetUserByEmail(payload.Email)
	if err != nil {
		var resp struct {
			Error   bool   `json:"error"`
			Message string `json:"message"`
		}
		resp.Error = true
		resp.Message = "No matching email found on our system"
		app.writeJSON(w, http.StatusAccepted, resp)
		return
	}

	link := fmt.Sprintf("%s/reset-password?email=%s", app.config.frontend, payload.Email)

	sign := urlsigner.Signer{
		Secret: []byte(app.config.secretKey),
	}

	signedLink := sign.GenerateTokenFromString(link)

	var data struct {
		Link string
	}
	data.Link = signedLink

	// send mail
	err = app.sendMail("info@widgets.com", payload.Email, "Password Reset Request", "password-reset", data)
	if err != nil {
		app.errorLog.Println(err)
		app.badRequest(w, r, err)
		return
	}

	var resp struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}

	resp.Error = false

	app.writeJSON(w, http.StatusCreated, resp)

}

func (app *application) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	err := app.readJSON(w, r, &payload)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	encryptor := encryption.Encryption{
		Key: []byte(app.config.secretKey),
	}

	email, err := encryptor.Decrypt(payload.Email)
	if err != nil {
		app.errorLog.Println(w, r, err)
		return
	}

	user, err := app.DB.GetUserByEmail(email)
	if err != nil {
		app.serverError(w, r, err)
		return
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte(payload.Password), 12)
	if err != nil {
		app.serverError(w, r, err)
		return
	}

	err = app.DB.UpdatePasswordForUser(user, string(newHash))
	if err != nil {
		app.serverError(w, r, err)
		return
	}

	var resp struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}
	resp.Error = false
	resp.Message = "password changed"

	err = app.writeJSON(w, http.StatusCreated, resp)
	if err != nil {
		app.serverError(w, r, err)
		return
	}
}

func (app *application) AllSales(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		PageSize    int `json:"page_size"`
		CurrentPage int `json:"current_page"`
	}

	err := app.readJSON(w, r, &payload)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	allSales, lastPage, totalRecords, err := app.DB.GetAllOrdersPaginated(payload.PageSize, payload.CurrentPage)
	if err != nil {
		app.errorLog.Println(err)
		app.serverError(w, r, err)
		return
	}

	resp := struct {
		CurrentPage  int             `json:"current_page"`
		PageSize     int             `json:"page_size"`
		LastPage     int             `json:"last_page"`
		TotalRecords int             `json:"total_records"`
		Orders       []*models.Order `json:"orders"`
	}{
		CurrentPage:  payload.CurrentPage,
		PageSize:     payload.PageSize,
		LastPage:     lastPage,
		TotalRecords: totalRecords,
		Orders:       allSales,
	}

	app.writeJSON(w, http.StatusOK, resp)
}

func (app *application) AllSubscriptions(w http.ResponseWriter, r *http.Request) {
	allSales, err := app.DB.GetAllSubscriptions()
	if err != nil {
		app.errorLog.Println(err)
		app.serverError(w, r, err)
		return
	}

	app.writeJSON(w, http.StatusOK, allSales)
}

func (app *application) GetSale(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	orderID, err := strconv.Atoi(id)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	order, err := app.DB.GetOrderByID(orderID)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	app.writeJSON(w, http.StatusOK, order)
}

func (app *application) RefundCharge(w http.ResponseWriter, r *http.Request) {
	var chargeToRefund struct {
		ID            int    `json:"id"`
		PaymentIntent string `json:"pi"`
		Amount        int    `json:"amount"`
		Currency      string `json:"currency"`
	}

	err := app.readJSON(w, r, &chargeToRefund)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	// optionally validate the amount against the underlaying order

	card := cards.Card{
		Secret:   app.config.stripe.secret,
		Key:      app.config.stripe.key,
		Currency: chargeToRefund.Currency,
	}

	err = card.Refund(chargeToRefund.PaymentIntent, chargeToRefund.Amount)
	if err != nil {
		app.serverError(w, r, err)
		return
	}

	// update status in database
	err = app.DB.UpdateOrderStatus(chargeToRefund.ID, models.StatusRefunded)
	if err != nil {
		app.badRequest(w, r, errors.New("the charge was refunded, but the database could not be updated"))
		return
	}

	var resp struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}
	resp.Error = false
	resp.Message = "Charge refunded"

	app.writeJSON(w, http.StatusOK, resp)
}

func (app *application) CancelSubscription(w http.ResponseWriter, r *http.Request) {
	var subToCancel struct {
		ID            int    `json:"id"`
		PaymentIntent string `json:"pi"`
		Currency      string `json:"currency"`
	}

	err := app.readJSON(w, r, &subToCancel)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	card := cards.Card{
		Secret:   app.config.stripe.secret,
		Key:      app.config.stripe.key,
		Currency: subToCancel.Currency,
	}

	err = card.CancelSubscription(subToCancel.PaymentIntent)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	// update status in database
	err = app.DB.UpdateOrderStatus(subToCancel.ID, models.StatusCancelled)
	if err != nil {
		app.serverError(w, r, errors.New("the subscription was canceled, but the database could not be updated"))
		return
	}

	var resp struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}
	resp.Error = false
	resp.Message = "Subscription canceled"

	app.writeJSON(w, http.StatusOK, resp)
}

func (app *application) AllUsers(w http.ResponseWriter, r *http.Request) {
	allUsers, err := app.DB.GetAllUsers()
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	app.writeJSON(w, http.StatusOK, allUsers)
}

func (app *application) OneUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, err := strconv.Atoi(id)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	user, err := app.DB.GetOneUser(userID)
	if err != nil {
		app.serverError(w, r, err)
		return
	}

	app.writeJSON(w, http.StatusOK, user)
}

func (app *application) EditUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, err := strconv.Atoi(id)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	var user models.User

	err = app.readJSON(w, r, &user)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	if userID > 0 {
		err = app.DB.EditUser(user)
		if err != nil {
			app.serverError(w, r, err)
			return
		}

		if user.Password != "" {
			newHash, err := bcrypt.GenerateFromPassword([]byte(user.Password), 12)
			if err != nil {
				app.serverError(w, r, err)
				return
			}

			err = app.DB.UpdatePasswordForUser(user, string(newHash))
			if err != nil {
				app.serverError(w, r, err)
				return
			}
		}
	} else {
		newHash, err := bcrypt.GenerateFromPassword([]byte(user.Password), 12)
		if err != nil {
			app.serverError(w, r, err)
			return
		}

		err = app.DB.AddUser(user, string(newHash))
		if err != nil {
			app.serverError(w, r, err)
			return
		}
	}

	var resp struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}

	resp.Error = false
	app.writeJSON(w, http.StatusOK, resp)
}

func (app *application) DeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	userID, err := strconv.Atoi(id)
	if err != nil {
		app.badRequest(w, r, err)
		return
	}

	err = app.DB.DeleteUser(userID)
	if err != nil {
		app.serverError(w, r, err)
		return
	}

	var resp struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}

	resp.Error = false
	app.writeJSON(w, http.StatusOK, resp)
}
