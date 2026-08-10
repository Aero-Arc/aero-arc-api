package httpapi

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestUSSJWTAuthorizerValidatesScopeAndSubject(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &USSJWTAuthorizer{publicKey: &privateKey.PublicKey, issuer: "issuer", audience: "aero-arc"}
	claims := ussClaims{
		Scope: ussStrategicCoordinationScope,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "issuer", Subject: "peer-uss", Audience: jwt.ClaimStrings{"aero-arc"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/uss/v1/operational_intents/id", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	subject, err := authorizer.Authorize(request, ussStrategicCoordinationScope)
	if err != nil || subject != "peer-uss" {
		t.Fatalf("Authorize = %q, %v", subject, err)
	}
	if _, err := authorizer.Authorize(request, "different.scope"); err == nil {
		t.Fatal("Authorize accepted a token without the required scope")
	}
}
