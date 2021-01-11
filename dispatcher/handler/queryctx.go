//     Copyright (C) 2020-2021, IrineSistiana
//
//     This file is part of mosdns.
//
//     mosdns is free software: you can redistribute it and/or modify
//     it under the terms of the GNU General Public License as published by
//     the Free Software Foundation, either version 3 of the License, or
//     (at your option) any later version.
//
//     mosdns is distributed in the hope that it will be useful,
//     but WITHOUT ANY WARRANTY; without even the implied warranty of
//     MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
//     GNU General Public License for more details.
//
//     You should have received a copy of the GNU General Public License
//     along with this program.  If not, see <https://www.gnu.org/licenses/>.

package handler

import (
	"context"
	"fmt"
	"github.com/miekg/dns"
	"go.uber.org/zap"
	"net"
	"sync/atomic"
	"time"
)

// Context is a query context that pass through plugins
// A Context will always have a non-nil Q.
// Context MUST be created by NewContext.
type Context struct {
	// init at beginning
	q         *dns.Msg
	from      net.Addr
	info      string // a short Context summary for logging
	id        uint32 // additional uint to distinguish duplicated msg
	startTime time.Time

	status ContextStatus
	r      *dns.Msg

	deferrable  []Executable
	deferAtomic uint32
}

type ContextStatus uint8

const (
	ContextStatusWaitingResponse ContextStatus = iota
	ContextStatusResponded
	ContextStatusServerFailed
	ContextStatusDropped
	ContextStatusRejected
)

var statusToStr = map[ContextStatus]string{
	ContextStatusWaitingResponse: "waiting response",
	ContextStatusResponded:       "responded",
	ContextStatusServerFailed:    "server failed",
	ContextStatusDropped:         "dropped",
	ContextStatusRejected:        "rejected",
}

func (status ContextStatus) String() string {
	s, ok := statusToStr[status]
	if ok {
		return s
	}
	return fmt.Sprintf("invalid status %d", status)
}

var id uint32

// NewContext creates a new query Context.
// q is the query dns msg. it cannot be nil, or NewContext will panic.
// from is the client net.Addr. It can be nil.
func NewContext(q *dns.Msg, from net.Addr) *Context {
	if q == nil {
		panic("handler: query msg is nil")
	}

	ctx := &Context{
		q:         q,
		from:      from,
		id:        atomic.AddUint32(&id, 1),
		startTime: time.Now(),

		status: ContextStatusWaitingResponse,
	}

	if len(q.Question) == 1 {
		q := q.Question[0]
		ctx.info = fmt.Sprintf("%s %d %d %d %d", q.Name, q.Qtype, q.Qclass, ctx.q.Id, ctx.id)
	} else {
		ctx.info = fmt.Sprintf("%v %d %d", ctx.q.Question, ctx.id, ctx.q.Id)
	}

	return ctx
}

// Q returns the query msg. It always returns a non-nil msg.
func (ctx *Context) Q() *dns.Msg {
	return ctx.q
}

// From returns the client net.Addr. It might be nil.
func (ctx *Context) From() net.Addr {
	return ctx.from
}

func (ctx *Context) R() *dns.Msg {
	return ctx.r
}

func (ctx *Context) Status() ContextStatus {
	return ctx.status
}

func (ctx *Context) SetResponse(r *dns.Msg, status ContextStatus) {
	ctx.r = r
	ctx.status = status
}

// CopyDeferFrom copies defer Executable from other Context.
func (ctx *Context) CopyDeferFrom(src *Context) {
	ctx.deferrable = make([]Executable, len(src.deferrable))
	copy(ctx.deferrable, src.deferrable)
}

// DeferExec registers an deferred Executable at this Context.
func (ctx *Context) DeferExec(e Executable) {
	if i := atomic.LoadUint32(&ctx.deferAtomic); i == 1 {
		panic("handler Context: concurrent ExecDefer or DeferExec")
	}
	ctx.deferrable = append(ctx.deferrable, e)
}

// ExecDefer executes all deferred Executable registered by DeferExec.
func (ctx *Context) ExecDefer(cCtx context.Context) error {
	if ok := atomic.CompareAndSwapUint32(&ctx.deferAtomic, 0, 1); !ok {
		panic("handler Context: concurrent ExecDefer or DeferExec")
	}
	defer atomic.CompareAndSwapUint32(&ctx.deferAtomic, 1, 0)

	for range ctx.deferrable {
		executable := ctx.deferrable[len(ctx.deferrable)-1]
		ctx.deferrable[len(ctx.deferrable)-1] = nil
		ctx.deferrable = ctx.deferrable[0 : len(ctx.deferrable)-1]
		if err := executable.Exec(cCtx, ctx); err != nil {
			return err
		}
	}
	return nil
}

// Id returns the Context id.
// Note: This id is not the dns msg id.
// It's a unique uint32 growing with the number of query.
func (ctx *Context) Id() uint32 {
	return ctx.id
}

// StartTime returns the time when the Context was created.
func (ctx *Context) StartTime() time.Time {
	return ctx.startTime
}

// InfoField returns a zap.Field.
// Just for convenience.
func (ctx *Context) InfoField() zap.Field {
	return zap.String("query", ctx.info)
}

// Copy deep copies this Context.
// Note that Copy won't copy registered deferred Executable.
// To copy them, use CopyDeferFrom after Copy.
func (ctx *Context) Copy() *Context {
	newCtx := new(Context)

	newCtx.q = ctx.q.Copy()
	newCtx.from = ctx.from
	newCtx.info = ctx.info
	newCtx.id = ctx.id
	newCtx.startTime = ctx.startTime

	newCtx.status = ctx.status
	if ctx.r != nil {
		newCtx.r = ctx.r.Copy()
	}

	return newCtx
}
