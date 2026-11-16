package webtest

import (
	"context"

	"github.com/nebuloss/cafard/internal/messaging"
)

// StartMessaging builds the real messaging service over the stack, as
// serve does, and puts it in Deps (track D's chat pages need it). Call it
// before mounting anything that copies Deps.
func (e *Env) StartMessaging() *messaging.Service {
	e.T.Helper()
	o := messaging.Options{}
	o.Config.SetDefaults()
	o.Images.SetDefaults()
	e.Deps.Server.DataDir = e.T.TempDir()
	svc, err := messaging.New(context.Background(), e.Deps, o)
	if err != nil {
		e.T.Fatal(err)
	}
	e.Deps.Messaging, e.Deps.Blobs = svc, svc.Blobs()
	return svc
}
