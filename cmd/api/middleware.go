package main

import "net/http"

func (app *application) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := app.authenticateToken(r)
		if err != nil {
			app.invalidCredentials(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
