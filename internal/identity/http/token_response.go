package http

import (
	"net/http"

	"github.com/sl-bus/backend/internal/identity/service"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

type tokenPairResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Role         string `json:"role"`
	OperatorID   string `json:"operatorId,omitempty"`
}

func writeTokenPair(w http.ResponseWriter, status int, tokens *service.TokenPair) {
	httpx.WriteSuccess(w, status, tokenPairResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		Role:         string(tokens.Role),
		OperatorID:   tokens.OperatorID,
	}, nil)
}
