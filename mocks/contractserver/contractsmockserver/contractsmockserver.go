// Package contractsmockserver implements a mocked version of the Contracts Server backend.
// DO NOT USE IN PRODUCTION
package contractsmockserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path"
	"sync"
	"time"

	"github.com/canonical/ubuntu-pro-for-windows/contractsapi"
)

const (
	//nolint:gosec // G101 false positive, this is not a credential
	// DefaultADToken is the value returned by default to the GET /token request, encoded in a JSON object.
	DefaultADToken = "eHy_ADToken"
	//nolint:gosec // G101 false positive, this is not a credential
	// DefaultProToken is the value returned by default to the POST /susbcription request, encoded in a JSON object.
	DefaultProToken = "CHx_ProToken"
)

// Server is a mock of the contract server, where its behaviour can be modified.
type Server struct {
	settings Settings

	server *http.Server
	mu     sync.RWMutex

	done chan struct{}
}

// Settings contains the parameters for the Server.
type Settings struct {
	Token        Endpoint
	Subscription Endpoint
	Address      string
}

// Endpoint contains settings for an API endpoint behaviour. Can be modified for testing purposes.
type Endpoint struct {
	// OnSuccess is the response returned in the happy path.
	OnSuccess Response

	// Disabled disables the endpoint.
	Disabled bool

	// Blocked means that a response will not be sent back, instead it'll block until the server is stopped.
	Blocked bool
}

// Response contains settings for an API endpoint response behaviour. Can be modified for testing purposes.
type Response struct {
	Value  string
	Status int
}

// DefaultSettings returns the default set of settings for the server.
func DefaultSettings() Settings {
	return Settings{
		Token:        Endpoint{OnSuccess: Response{Value: DefaultADToken, Status: http.StatusOK}},
		Subscription: Endpoint{OnSuccess: Response{Value: DefaultProToken, Status: http.StatusOK}},
		Address:      "localhost:0",
	}
}

// NewServer creates a new contract server with the provided settings.
func NewServer(s Settings) *Server {
	return &Server{
		settings: s,
	}
}

// Stop stops the server.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server == nil {
		return errors.New("already stopped")
	}

	err := s.server.Close()
	<-s.done

	s.server = nil

	return err
}

// Serve starts a new HTTP server mocking the Contracts Server backend REST API with
// responses defined according to Server Settings. Use Stop to Stop the server and
// release resources.
func (s *Server) Serve(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.server != nil {
		return "", errors.New("already serving")
	}

	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", s.settings.Address)
	if err != nil {
		return "", fmt.Errorf("failed to listen over tcp: %v", err)
	}

	mux := http.NewServeMux()

	if !s.settings.Token.Disabled {
		mux.HandleFunc(path.Join(contractsapi.Version, contractsapi.TokenPath), s.handleToken)
	}

	if !s.settings.Subscription.Disabled {
		mux.HandleFunc(path.Join(contractsapi.Version, contractsapi.SubscriptionPath), s.handleSubscription)
	}

	s.server = &http.Server{
		Addr:              lis.Addr().String(),
		Handler:           mux,
		ReadHeaderTimeout: 3 * time.Second,
	}

	s.done = make(chan struct{})

	go func() {
		defer close(s.done)
		if err := s.server.Serve(lis); err != nil && err != http.ErrServerClosed {
			slog.Error("Failed to start the HTTP server", "error", err)
		}
	}()

	return lis.Addr().String(), nil
}

// handleToken implements the /token endpoint.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := s.handle(w, r, http.MethodGet, s.settings.Token); err != nil {
		fmt.Fprintf(w, "%v", err)
		return
	}

	if _, err := fmt.Fprintf(w, `{%q: %q}`, contractsapi.ADTokenKey, s.settings.Token.OnSuccess.Value); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "failed to write the response: %v", err)
		return
	}
}

// handleSubscription implements the /susbcription endpoint.
func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	if err := s.handle(w, r, http.MethodPost, s.settings.Subscription); err != nil {
		fmt.Fprintf(w, "%v", err)
		return
	}

	var data map[string]string
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "Bad Request")
		return
	}

	userJWT, ok := data[contractsapi.JWTKey]
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "JSON payload does not contain the expected key")
		return
	}

	if len(userJWT) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, "JWT cannot be empty")
		return
	}

	if _, err := fmt.Fprintf(w, `{%q: %q}`, contractsapi.ProTokenKey, s.settings.Subscription.OnSuccess.Value); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "failed to write the response: %v", err)
		return
	}
}

// handle extracts common boilerplate from endpoints.
func (s *Server) handle(w http.ResponseWriter, r *http.Request, wantMethod string, endpoint Endpoint) (err error) {
	slog.Info("Received request", "endpoint", r.URL.Path, "method", r.Method)
	defer func() {
		if err != nil {
			slog.Error("bad request", "error", err, "endpoint", r.URL.Path, "method", r.Method)
		}
	}()

	if r.Method != wantMethod {
		w.WriteHeader(http.StatusBadRequest)
		return fmt.Errorf("this endpoint only supports %s", wantMethod)
	}

	if endpoint.Blocked {
		<-s.done
		slog.Debug("Server context was cancelled. Exiting", "endpoint", r.URL.Path)
		return errors.New("server stopped")
	}

	if endpoint.OnSuccess.Status != 200 {
		w.WriteHeader(endpoint.OnSuccess.Status)
		return fmt.Errorf("mock error: %d", endpoint.OnSuccess.Status)
	}

	return nil
}
