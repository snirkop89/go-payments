package main

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
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

func (app *application) CreateDirIfNotExist(path string) error {
	const mode = 0755
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		err := os.MkdirAll(path, mode)
		if err != nil {
			return err
		}
	}
	return nil
}
