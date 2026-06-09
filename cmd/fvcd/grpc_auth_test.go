package main

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGRPCAuthAuthorizeRequiresBearerToken(t *testing.T) {
	auth := grpcAuth{token: "secret"}

	if err := auth.authorize(context.Background()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated without token, got %v", err)
	}

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer secret"))
	if err := auth.authorize(ctx); err != nil {
		t.Fatalf("expected bearer token to authorize: %v", err)
	}
}
