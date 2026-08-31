package api

import (
	"net/http"

	"example.com/semanticmini/domain"
)

type Router interface {
	GET(string, http.HandlerFunc)
}

type Server struct {
	store domain.OrderStore
}

func (s *Server) InstallRoutes(router Router) {
	router.GET("/orders/:id", s.GetOrder)
}

func (s *Server) GetOrder(w http.ResponseWriter, r *http.Request) {
	_, _ = domain.LoadOrder(r.Context(), s.store, r.PathValue("id"))
	w.WriteHeader(http.StatusOK)
}
