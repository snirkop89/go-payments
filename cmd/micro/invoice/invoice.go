package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

const version = "1.0.0"

type config struct {
	port int
	smtp struct {
		host     string
		port     int
		username string
		password string
	}
	frontend string
}

type application struct {
	config   config
	infoLog  *log.Logger
	errorLog *log.Logger
	version  string
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

	app.infoLog.Printf("Starting Backend server on port %d\n", app.config.port)
	return srv.ListenAndServe()
}

func main() {
	var cfg config

	flag.IntVar(&cfg.port, "port", 5000, "Server port to listen on")
	flag.StringVar(&cfg.smtp.host, "smtp-host", "smtp.mailtrap.io", "FQDN of the SMTP server")
	flag.StringVar(&cfg.smtp.username, "smtp-user", "693fa0c1373000", "SMTP user")
	flag.StringVar(&cfg.smtp.password, "smtp-password", "93a61a75eb3840", "SMTP user password")
	flag.IntVar(&cfg.smtp.port, "smtp-port", 587, "SMTP server port")
	flag.StringVar(&cfg.frontend, "frontend", "http://localhost:4000", "Frontend URL")

	flag.Parse()

	infoLog := log.New(os.Stdout, "INFO\t", log.Ldate|log.Ltime)
	errorLog := log.New(os.Stdout, "ERROR\t", log.Ldate|log.Ltime|log.Lshortfile)

	app := &application{
		config:   cfg,
		infoLog:  infoLog,
		errorLog: errorLog,
		version:  version,
	}

	err := app.CreateDirIfNotExist("./invoices")
	if err != nil {
		errorLog.Println(err)
		os.Exit(1)
	}

	err = app.serve()
	if err != nil {
		errorLog.Println(err)
		os.Exit(1)
	}
}
