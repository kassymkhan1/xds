package config

import (
	"errors"
	"fmt"

	cluster "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpoint "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listener "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	route "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	tls "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
)

// validatable is implemented by every go-control-plane resource: the
// protoc-gen-validate rules from Envoy's own API definitions.
type validatable interface{ ValidateAll() error }

// ValidateClusters checks clusters against Envoy's proto rules and for
// unique, non-empty names.
func ValidateClusters(items []*cluster.Cluster) error {
	return validate("cluster", items, (*cluster.Cluster).GetName)
}

// ValidateEndpoints checks ClusterLoadAssignments against Envoy's proto
// rules and for unique, non-empty cluster names.
func ValidateEndpoints(items []*endpoint.ClusterLoadAssignment) error {
	return validate("endpoint cluster_name", items, (*endpoint.ClusterLoadAssignment).GetClusterName)
}

// ValidateListeners checks listeners against Envoy's proto rules and for
// unique, non-empty names.
func ValidateListeners(items []*listener.Listener) error {
	return validate("listener", items, (*listener.Listener).GetName)
}

// ValidateRoutes checks RouteConfigurations against Envoy's proto rules and
// for unique, non-empty names.
func ValidateRoutes(items []*route.RouteConfiguration) error {
	return validate("route", items, (*route.RouteConfiguration).GetName)
}

// ValidateSecrets checks secrets against Envoy's proto rules and for
// unique, non-empty names.
func ValidateSecrets(items []*tls.Secret) error {
	return validate("secret", items, (*tls.Secret).GetName)
}

func validate[T validatable](kind string, items []T, name func(T) string) error {
	var errs []error
	seen := make(map[string]bool, len(items))

	for i, item := range items {
		n := name(item)
		switch {
		case n == "":
			errs = append(errs, fmt.Errorf("%s %d: missing name", kind, i))
		case seen[n]:
			errs = append(errs, fmt.Errorf("duplicate %s name %q", kind, n))
		}
		seen[n] = true

		if err := item.ValidateAll(); err != nil {
			errs = append(errs, fmt.Errorf("%s %q: %w", kind, n, err))
		}
	}
	return errors.Join(errs...)
}
