package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"golang.org/x/crypto/bcrypt"
)

func (app *application) readJSON(w http.ResponseWriter, r *http.Request, data any) error {
	maxBytes := 1048576 // 1MB

	r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))

	dec := json.NewDecoder(r.Body)
	err := dec.Decode(data)
	if err != nil {
		return err
	}

	err = dec.Decode(&struct{}{})
	if err != io.EOF {
		return errors.New("body must contain only a single JSON value")
	}

	return nil
}

// writeJSON writes arbitrary data out as JSON
func (app *application) writeJSON(w http.ResponseWriter, status int, data any, headers ...http.Header) error {
	out, err := json.MarshalIndent(data, "", "\t")
	if err != nil {
		return err
	}

	if len(headers) > 0 {
		for k, v := range headers[0] {
			w.Header()[k] = v
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(out)

	return nil
}

func (app *application) badRequest(w http.ResponseWriter, r *http.Request, err error) error {
	var payload struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}

	payload.Error = true
	payload.Message = err.Error()

	return app.writeJSON(w, http.StatusBadRequest, payload)
}

func (app *application) serverError(w http.ResponseWriter, r *http.Request, err error) error {
	var payload struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}

	payload.Error = true
	payload.Message = err.Error()

	return app.writeJSON(w, http.StatusInternalServerError, payload)
}

func (app *application) invalidCredentials(w http.ResponseWriter) error {
	var payload struct {
		Error   bool   `json:"error"`
		Message string `json:"message"`
	}

	payload.Error = true
	payload.Message = "invalid authentication credentials"

	return app.writeJSON(w, http.StatusUnauthorized, payload)
}

func (app *application) passwordMatches(hash, password string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		switch {
		case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
			return false, nil
		default:
			return false, err
		}
	}

	return true, nil
}

func (app *application) failedValidation(w http.ResponseWriter, r *http.Request, errors map[string]string) {
	var payload struct {
		Error   bool              `json:"error"`
		Message string            `json:"message"`
		Errors  map[string]string `json:"errors"`
	}

	payload.Error = true
	payload.Message = "failed validation"
	payload.Errors = errors

	app.writeJSON(w, http.StatusUnprocessableEntity, payload)
}
