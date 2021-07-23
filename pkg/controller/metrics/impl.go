package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
)

type defaultImpl struct{}

// The impl interface is borrowed from the security-profiles-operator metrics code, with the generated fake_impl.go
// file from the below generate command. Preserved in case we will ever need to regenerate (not likely).
// // go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 -generate
// // counterfeiter:generate . impl
type impl interface {
	Register(c prometheus.Collector) error
	ListenAndServe(addr string, handler http.Handler) error
}

func (d *defaultImpl) Register(c prometheus.Collector) error {
	return prometheus.Register(c)
}

func (d *defaultImpl) ListenAndServe(addr string, handler http.Handler) error {
	return http.ListenAndServe(addr, handler)
}
