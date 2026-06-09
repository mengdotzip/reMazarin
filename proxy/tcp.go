package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/mdobak/go-xerrors"
)

// startTCPProxy launches a raw TCP listener for the given port→target mapping.
// If a listener already exists on that port it is stopped first.
func (p *Proxy) startTCPProxy(port, target, routeUrl string) {
	if p.ctx == nil {
		slog.Error("tcp proxy: context not initialized", "port", port)
		return
	}

	p.tcpMu.Lock()
	if old, exists := p.tcpCancels[port]; exists {
		old()
	}
	ctx, cancel := context.WithCancel(p.ctx)
	p.tcpCancels[port] = cancel
	p.tcpMu.Unlock()

	p.Wg.Add(1)
	go func() {
		defer p.Wg.Done()
		runTCPProxy(ctx, port, target, routeUrl, p.ErrChan)
	}()
}

// stopTCPProxy cancels the listener on the given port.
func (p *Proxy) stopTCPProxy(port string) {
	p.tcpMu.Lock()
	cancel, ok := p.tcpCancels[port]
	if ok {
		delete(p.tcpCancels, port)
	}
	p.tcpMu.Unlock()
	if ok {
		cancel()
	}
}

func runTCPProxy(ctx context.Context, port, target, routeUrl string, errChan chan error) {
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		errChan <- xerrors.Newf("tcp listen on port %s: %w", port, err)
		return
	}
	defer ln.Close()

	slog.Info("tcp proxy started", "port", port, "target", target)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var connWg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			slog.Error("tcp accept error", "port", port, "error", err)
			break
		}
		connWg.Add(1)
		go func() {
			defer connWg.Done()
			handleTCPConn(ctx, conn, target, routeUrl)
		}()
	}
	connWg.Wait()
	slog.Info("tcp proxy stopped", "port", port)
}

func handleTCPConn(ctx context.Context, clientConn net.Conn, targetAddr, routeUrl string) {
	defer clientConn.Close()
	clientIP, _, _ := net.SplitHostPort(clientConn.RemoteAddr().String())

	authorized, accessUser := authorizeIP(routeUrl, clientIP)
	if !authorized {
		logAccess(clientIP, "Unauthorized User", routeUrl)
		slog.Warn("tcp: connection rejected, not authorized", "client", clientIP, "route", routeUrl)
		return
	}

	RecordRequest(routeUrl)
	if authStore != nil {
		logAccess(clientIP, accessUser, routeUrl)
	}
	targetConn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		slog.Error("tcp: failed to connect to target", "target", targetAddr, "client", clientIP, "error", err)
		return
	}
	defer targetConn.Close()

	copyCtx, cancelCopy := context.WithCancel(ctx)
	defer cancelCopy()

	// Close both connections when copy context is done (shutdown or half-close).
	go func() {
		<-copyCtx.Done()
		clientConn.Close()
		targetConn.Close()
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer cancelCopy()
		io.Copy(targetConn, clientConn)
	}()
	go func() {
		defer wg.Done()
		defer cancelCopy()
		io.Copy(clientConn, targetConn)
	}()

	wg.Wait()
	slog.Debug("tcp: connection closed", "client", clientIP, "target", targetAddr)
}
