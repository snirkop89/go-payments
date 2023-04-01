package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/snirkop89/go-payments/internal/driver"
	"github.com/snirkop89/go-payments/internal/models"
)

const version = "1.0.0"

type config struct {
	port int
	env  string
	db   struct {
		dsn string
	}
	stripe struct {
		secret string
		key    string
	}
	smtp struct {
		host     string
		port     int
		username string
		password string
	}
	secretKey string
	frontend  string
}

type application struct {
	config   config
	infoLog  *log.Logger
	errorLog *log.Logger
	version  string
	DB       models.DBModel
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

	app.infoLog.Printf("Starting Backend server in %s mode on port %d\n", app.config.env, app.config.port)
	return srv.ListenAndServe()
}

func main() {
	var cfg config

	flag.IntVar(&cfg.port, "port", 4001, "Server port to listen on")
	flag.StringVar(&cfg.env, "env", "development", "Application environemt {development|production|maintenance}")
	flag.StringVar(&cfg.db.dsn, "dsn", "snir:secret@(localhost:3306)/widgets?parseTime=true&tls=false", "Connection string to the database")
	flag.StringVar(&cfg.smtp.host, "smtp-host", "smtp.mailtrap.io", "FQDN of the SMTP server")
	flag.StringVar(&cfg.smtp.username, "smtp-user", "693fa0c1373000", "SMTP user")
	flag.StringVar(&cfg.smtp.password, "smtp-password", "93a61a75eb3840", "SMTP user password")
	flag.IntVar(&cfg.smtp.port, "smtp-port", 587, "SMTP server port")
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

	app := &application{
		config:   cfg,
		infoLog:  infoLog,
		errorLog: errorLog,
		version:  version,
		DB:       models.DBModel{DB: conn},
	}

	err = app.serve()
	if err != nil {
		errorLog.Println(err)
		os.Exit(1)
	}
}
