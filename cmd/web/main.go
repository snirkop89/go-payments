package main

import (
	"encoding/gob"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/alexedwards/scs/mysqlstore"
	"github.com/alexedwards/scs/v2"
	"github.com/snirkop89/go-payments/internal/driver"
	"github.com/snirkop89/go-payments/internal/models"
)

const version = "1.0.0"
const cssVersion = "1"

var session *scs.SessionManager

type config struct {
	port int
	env  string
	api  string
	db   struct {
		dsn string
	}
	stripe struct {
		secret string
		key    string
	}
	secretKey string
	frontend  string
}

type application struct {
	config        config
	infoLog       *log.Logger
	errorLog      *log.Logger
	templateCache map[string]*template.Template
	version       string
	DB            models.DBModel
	session       *scs.SessionManager
}

func (app *application) serve() error {
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", app.config.port),
		Handler:           app.routes(),
		IdleTimeout:       30 * time.Second,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}

	app.infoLog.Printf("Starting HTTP server in %s mode on port %d\n", app.config.env, app.config.port)
	return srv.ListenAndServe()
}

func main() {
	gob.Register(TransactionData{})
	var cfg config

	flag.IntVar(&cfg.port, "port", 4000, "Server port to listen on")
	flag.StringVar(&cfg.env, "env", "development", "Application environemt {development|production}")
	flag.StringVar(&cfg.db.dsn, "dsn", "snir:secret@(localhost:3306)/widgets?parseTime=true&tls=false", "Connection string to the database")
	flag.StringVar(&cfg.api, "api", "http://localhost:4001", "URL to api")
	flag.StringVar(&cfg.secretKey, "secret", "BBE8VkcWUuX93kKM3GiGuu8LQyGwkX5b", "secret key")
	flag.StringVar(&cfg.frontend, "frontend", "http://localhost:4000", "Frontend URL")
	flag.Parse()

	cfg.stripe.key = os.Getenv("STRIPE_KEY")
	cfg.stripe.secret = os.Getenv("STRIPE_SECRET")

	infoLog := log.New(os.Stdout, "INFO\t", log.Ldate|log.Ltime)
	errorLog := log.New(os.Stdout, "ERROR\t", log.Ldate|log.Ltime|log.Lshortfile)

	conn, err := driver.OpenDB(cfg.db.dsn)
	if err != nil {
		errorLog.Fatal(err)
	}
	defer conn.Close()

	// set up session
	session = scs.New()
	session.Lifetime = 12 * time.Hour
	session.Store = mysqlstore.New(conn)

	tc := make(map[string]*template.Template)

	app := &application{
		config:        cfg,
		infoLog:       infoLog,
		errorLog:      errorLog,
		templateCache: tc,
		version:       version,
		DB:            models.DBModel{DB: conn},
		session:       session,
	}

	go app.ListenToWsChannel()

	err = app.serve()
	if err != nil {
		errorLog.Println(err)
		os.Exit(1)
	}
}
