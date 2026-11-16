package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nebuloss/cafard/internal/auth"
	"github.com/nebuloss/cafard/internal/dto"
)

// ResourceHandler reads one resource for a principal. Return
// mcp.ResourceNotFoundError(uri) for something that does not exist or is
// not visible to p.
type ResourceHandler func(ctx context.Context, p auth.Principal, uri string) (*mcp.ReadResourceResult, error)

// Subscriptions is told when a session subscribes to a resource or stops.
// Subscribe may refuse (an unknown URI).
type Subscriptions interface {
	Subscribe(ctx context.Context, p auth.Principal, uri string) error
	Unsubscribe(p auth.Principal, uri string)
}

type resourceDef struct {
	scope auth.Scope
	add   func(srv *mcp.Server)
}

type principalServer struct {
	principal string
	srv       *mcp.Server
}

func (s *Server) wrapResource(scope auth.Scope, h ResourceHandler) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		var p auth.Principal
		ok := false
		if req.Extra != nil && req.Extra.TokenInfo != nil {
			p, ok = req.Extra.TokenInfo.Extra[principalKey].(auth.Principal)
		}
		if !ok || !p.Has(scope) {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		res, err := h(auth.WithPrincipal(ctx, p), p, req.Params.URI)
		var apiErr *dto.APIError
		if errors.As(err, &apiErr) && apiErr.Code == dto.CodeNotFound {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		if err != nil && ctx.Err() == nil && apiErr == nil {
			s.deps.Log.Error("resource failed", "uri", req.Params.URI, "principal", p.Name, "err", err)
		}
		return res, err
	}
}

func (s *Server) addResourceDef(d resourceDef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealed {
		panic("mcpserver: resource added after the server started serving")
	}
	s.resources = append(s.resources, d)
}

// AddResource registers a fixed resource that needs scope. Call it before
// serving.
func (s *Server) AddResource(scope auth.Scope, r mcp.Resource, h ResourceHandler) {
	handler := s.wrapResource(scope, h)
	s.addResourceDef(resourceDef{scope: scope, add: func(srv *mcp.Server) {
		res := r
		srv.AddResource(&res, handler)
	}})
}

// AddResourceTemplate registers a templated resource that needs scope.
func (s *Server) AddResourceTemplate(scope auth.Scope, t mcp.ResourceTemplate, h ResourceHandler) {
	handler := s.wrapResource(scope, h)
	s.addResourceDef(resourceDef{scope: scope, add: func(srv *mcp.Server) {
		tmpl := t
		srv.AddResourceTemplate(&tmpl, handler)
	}})
}

// SetSubscriptions turns on resource subscriptions. Call it before serving.
func (s *Server) SetSubscriptions(sub Subscriptions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealed {
		panic("mcpserver: SetSubscriptions called after the server started serving")
	}
	s.subs = sub
}

// ResourceUpdated tells the sessions of principal that subscribed to uri
// that it changed; an empty principal means every principal.
func (s *Server) ResourceUpdated(ctx context.Context, principal, uri string) {
	s.mu.Lock()
	var targets []*mcp.Server
	for _, ps := range s.servers {
		if principal == "" || ps.principal == principal {
			targets = append(targets, ps.srv)
		}
	}
	s.mu.Unlock()
	for _, srv := range targets {
		_ = srv.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: uri})
	}
}
