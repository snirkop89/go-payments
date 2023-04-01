package main

import "net/http"

func SessionLoad(next http.Handler) http.Handler {
	return session.LoadAndSave(next)
}

func (app *application) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.infoLog.Println("userid:", app.session.Get(r.Context(), "userID"))
		if !app.session.Exists(r.Context(), "userID") {
			http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			return
		}
		next.ServeHTTP(w, r)
	})
}
