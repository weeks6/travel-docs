package auth

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

var prefix = "auth"

func GetUrl(method string, url string) string {
	return fmt.Sprintf("%v /%v%v", method, prefix, url)
}

func RegisterHandlers() {
	http.HandleFunc(GetUrl("POST", "/token"), func(w http.ResponseWriter, r *http.Request) {

		token, err := createJWTToken(UserPayload{UserId: "test_user"})

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintln(w, "Token error")
			log.Printf("Token Signing error: %v\n", err)
			return
		}

		w.Header().Set("Content-Type", "application/jwt")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, token)
	})

	http.HandleFunc(GetUrl("POST", "/logout"), func(w http.ResponseWriter, r *http.Request) {
		auth_header := r.Header.Get("Authorization")
		token := strings.Split(auth_header, " ")[1]

		if auth_header == "" || token == "" {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintln(w, "No token in header")
			return
		}

	})
}
