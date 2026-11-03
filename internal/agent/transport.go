package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/acp-sdk/jsonrpc"
)

// transport connects the SDK to the agent's standard streams and delivers
// session/update notifications itself, synchronously and in wire order.
//
// SDK v0.6.0 dispatches every incoming message on its own goroutine, so it
// would apply streamed updates out of order, and a response could reach Mu
// before the notifications the agent sent ahead of it. ACP updates are
// ordered, and Mu relies on a load or prompt response following the updates
// it accounts for. Responses are read in this loop as well, so handling each
// update before reading the next message preserves both. Remove this when
// the SDK dispatches notifications in order.
type transport struct{ p *Process }

func (t transport) Connect(ctx context.Context) (acp.Connection, error) {
	conn, err := (&acp.IOTransport{Reader: t.p.stdout, Writer: t.p.stdin}).Connect(ctx)
	if err != nil {
		return nil, err
	}
	return orderedConn{conn, t.p}, nil
}

type orderedConn struct {
	acp.Connection
	p *Process
}

func (c orderedConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	for {
		msg, err := c.Connection.Read(ctx)
		if err != nil {
			c.p.endOne.Do(func() { close(c.p.ended) })
			return nil, err
		}
		request, ok := msg.(*jsonrpc.Request)
		if !ok || request.ID.IsValid() || request.Method != acp.MethodSessionUpdate {
			return msg, nil
		}
		var n acp.SessionNotification
		if err := json.Unmarshal(request.Params, &n); err != nil {
			fmt.Fprintf(c.p.diag, "failed to handle notification %s: %v\n", request.Method, err)
			continue
		}
		if c.p.hooks.Update != nil {
			c.p.hooks.Update(n)
		}
	}
}
