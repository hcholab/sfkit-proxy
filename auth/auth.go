package auth

import (
	"context"
	"log/slog"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"golang.org/x/oauth2/google"
)

func getGCPToken(ctx context.Context) (token string, err error) {
	creds, err := google.FindDefaultCredentials(ctx, "openid", "email", "profile")
	if err != nil {
		return
	}
	tok, err := creds.TokenSource.Token()
	if err != nil {
		return
	}
	token = tok.AccessToken
	return
}

func getAzureToken(ctx context.Context) (token string, err error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return
	}
	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/"},
	})
	if err != nil {
		return
	}
	token = tok.Token
	return
}

func GetDefaultCredentialToken(ctx context.Context) (token string, err error) {
	if token, err = getGCPToken(ctx); err != nil {
		slog.Warn(err.Error())
		token, err = getAzureToken(ctx)
	}
	return
}
