package authz

import (
	"context"
	"net/http"
)

// Request-context plumbing for the authenticated Principal.
//
// This lives next to Principal rather than in the guard package so that
// handlers can read the principal WITHOUT importing the guard — which is what
// keeps internal/machineauth (a handler package) free of a dependency on
// internal/authn (which imports internal/machineauth). One direction of that
// pair would otherwise be an import cycle.
//
// H19/H28 RESTATED AS A CODE PROPERTY: the only writer of this context value
// is the guard, and the guard derives it exclusively from a server-side record
// (session store, setup-token comparison, AUTHN_MACHINE lookup). There is no
// setter that takes a request body, a query parameter, or a header value, so
// "the client claimed to be X" has no representation in this codebase.

type principalCtxKey struct{}

// WithPrincipal attaches the authenticated principal to the request. Called by
// internal/authn's guard and by nothing else.
func WithPrincipal(r *http.Request, p Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalCtxKey{}, p))
}

// PrincipalFrom returns the authenticated principal, or ok == false when the
// route is one of the deliberately unauthenticated ones (POST /api/auth/*).
// A handler that needs an actor must treat !ok as an error, never as an
// anonymous default.
func PrincipalFrom(r *http.Request) (Principal, bool) {
	p, ok := r.Context().Value(principalCtxKey{}).(Principal)
	return p, ok
}
