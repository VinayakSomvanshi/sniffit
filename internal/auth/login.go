package auth

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed login.html
var loginFS embed.FS

var loginTmpl = template.Must(template.ParseFS(loginFS, "login.html"))

// ServeLoginPage renders the login page for browser requests.
func ServeLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	loginTmpl.Execute(w, nil)
}

// APIUnauthorized writes a JSON 401 response for API clients.
func APIUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"unauthorized","message":"` + message + `"}`))
}
