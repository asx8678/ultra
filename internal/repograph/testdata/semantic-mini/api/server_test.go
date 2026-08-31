package api

import (
	"net/http/httptest"
	"testing"
)

func TestGetOrder(t *testing.T) {
	server := &Server{}
	request := httptest.NewRequest("GET", "/orders/ord-7", nil)
	response := httptest.NewRecorder()
	server.GetOrder(response, request)
}
