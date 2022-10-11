package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"log"

	"github.com/gorilla/sessions"
)

var created bool = true

// GET /healthcheck
func healthCheck(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("app good to go"))
}

// POST /api/auth
// Takes the auth url/login token, and gets an auth token for the rCTF api
// Returns back the team name and 200 if successful, otherwise 403/500+
func authRequest(w http.ResponseWriter, r *http.Request, s *sessions.Session) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("error handling client auth, couldn't read body: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	bodyStr := string(body)
	parts := strings.Split(bodyStr, "/login?token=")
	loginTokenEncoded := parts[len(parts)-1]

	loginToken, err := url.QueryUnescape(loginTokenEncoded)
	if err != nil {
		log.Printf("error handling client auth, couldn't decode login token: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	authToken, err := authToRctf(loginToken)
	if err != nil {
		log.Printf("error handling client auth, couldn't auth to rCTF: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if authToken == "" {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// have a valid auth token, get team info
	userInfo, err := getUserInfo(authToken)
	if err != nil {
		log.Printf("error handling client auth, couldn't get user info from rCTF: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// save the team data to the user's session
	s.Values["teamName"] = userInfo.TeamName
	s.Values["id"] = userInfo.Id
	s.Values["authToken"] = authToken
	if err = s.Save(r, w); err != nil {
		log.Printf("error handling client auth, couldn't save the session: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// send back the team name
	w.Write([]byte(userInfo.TeamName))
}

type StatusResponse struct {
	State string `json:"state"` // "active" || "inactive"
	Host  string `json:"host,omitempty"`
	// ExpTime
}

// GET /api/status
// Get the status of the team's deployment
func statusRequest(w http.ResponseWriter, r *http.Request, s *sessions.Session) {
	// make sure the session is valid
	if _, exists := s.Values["id"]; s.IsNew || !exists {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// TODO: check k8s for instance

	var resp StatusResponse

	if created {
		resp = StatusResponse{State: "active", Host: "1.2.3.4:8989"}
	} else {
		resp = StatusResponse{State: "inactive"}
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		log.Printf("error handling status request, couldn't marshal response data: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(respBytes)
}

type CreateInstanceResponse struct {
	Host string `json:"host"` // host:port string
	// ExpTime
}

// POST /api/create
// Create a deployment instance for the team
func createInstanceRequest(w http.ResponseWriter, r *http.Request, s *sessions.Session) {
	// make sure the session is valid
	if _, exists := s.Values["id"]; s.IsNew || !exists {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	log.Printf("Deploying instance for %s (ID: %s)\n", s.Values["teamName"], s.Values["id"])

	// TODO: create instance and store in memcache

	resp := CreateInstanceResponse{Host: "1.2.3.4:8989"}
	respBytes, err := json.Marshal(resp)
	if err != nil {
		log.Printf("error handling create instance request, couldn't marshal response data: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	created = true

	w.Header().Add("Content-type", "application/json")
	w.Write(respBytes)
}

// POST /api/extend
// Extend the timeout for a deployment instance
// Response on 200 is the new expiration timestamp
func extendInstanceRequest(w http.ResponseWriter, r *http.Request, s *sessions.Session) {
	// make sure the session is valid
	if _, exists := s.Values["id"]; s.IsNew || !exists {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	log.Printf("Extending instance for %s (ID: %s)\n", s.Values["teamName"], s.Values["id"])

	// TODO: extend instance and update memcache

	w.Header().Add("Content-type", "text/plain")
	w.Write([]byte("2022-01-01 12:34:56"))
}

// POST /api/destroy
// Destroy a deployment instance
// 200 means successfully destroy
func destroyInstanceRequest(w http.ResponseWriter, r *http.Request, s *sessions.Session) {
	// make sure the session is valid
	if _, exists := s.Values["id"]; s.IsNew || !exists {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	log.Printf("Destroying instance for %s (ID: %s)\n", s.Values["teamName"], s.Values["id"])

	// TODO: destroy instance and update memcache

	created = false

	w.WriteHeader(http.StatusOK)
}
