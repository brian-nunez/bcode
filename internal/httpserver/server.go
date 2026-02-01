package httpserver

import (
	"context"

	v1 "brian-nunez/bcode/internal/handlers/v1"
)

type Server interface {
	Start(addr string) error
	Shutdown(ctx context.Context) error
}

type BootstrapConfig struct {
	StaticDirectories map[string]string
}

func Bootstrap(config BootstrapConfig) Server {
	server := New().
		WithStaticAssets(config.StaticDirectories).
		WithDefaultMiddleware().
		WithErrorHandler().
		WithRoutes(v1.RegisterRoutes).
		WithNotFound().
		Build()

	return server
}
