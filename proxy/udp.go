package proxy

import (
	"context"
	"log/slog"
	"net"
	"reMazarin/storage"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mdobak/go-xerrors"
)

// udpSessionTimeout is how long a per-client UDP relay flow may sit idle (no
// packets in either direction) before its target socket is reaped. UDP is
// connectionless, so there is no close to observe — only inactivity.
const udpSessionTimeout = 120 * time.Second

// udpBufSize bounds a single datagram read (max UDP payload is 64 KiB).
const udpBufSize = 64 * 1024

// startUDPProxy launches a raw UDP relay for the given port→target mapping.
// If a relay already exists on that port it is stopped first.
func (p *Proxy) startUDPProxy(port, target, routeUrl string) {
	if p.ctx == nil {
		slog.Error("udp proxy: context not initialized", "port", port)
		return
	}

	p.udpMu.Lock()
	if old, exists := p.udpCancels[port]; exists {
		old()
	}
	ctx, cancel := context.WithCancel(p.ctx)
	p.udpCancels[port] = cancel
	p.udpMu.Unlock()

	p.Wg.Add(1)
	go func() {
		defer p.Wg.Done()
		runUDPProxy(ctx, port, target, routeUrl, p.ErrChan)
	}()
}

// stopUDPProxy cancels the relay on the given port.
func (p *Proxy) stopUDPProxy(port string) {
	p.udpMu.Lock()
	cancel, ok := p.udpCancels[port]
	if ok {
		delete(p.udpCancels, port)
	}
	p.udpMu.Unlock()
	if ok {
		cancel()
	}
}

// udpSession is one client's NAT-style relay flow: a dedicated socket dialed to
// the target, plus a last-activity timestamp used by the idle reaper.
type udpSession struct {
	targetConn net.Conn
	lastActive atomic.Int64 // unixnano; bumped on traffic in either direction
}

// runUDPProxy listens on a UDP port and relays datagrams to the target. Because
// UDP has no connections, it tracks a per-client session (keyed by source addr):
// client→target packets go out over that client's dialed target socket, and a
// per-session pump copies target→client replies back over the listen socket.
// Idle sessions are reaped after udpSessionTimeout.
func runUDPProxy(ctx context.Context, port, target, routeUrl string, errChan chan error) {
	listenConn, err := net.ListenPacket("udp", ":"+port)
	if err != nil {
		errChan <- xerrors.Newf("udp listen on port %s: %w", port, err)
		return
	}
	defer listenConn.Close()

	slog.Info("udp proxy started", "port", port, "target", target)

	go func() {
		<-ctx.Done()
		listenConn.Close()
	}()

	var (
		mu       sync.Mutex
		sessions = make(map[string]*udpSession)
		wg       sync.WaitGroup
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		reapUDPSessions(ctx, &mu, sessions)
	}()

	buf := make([]byte, udpBufSize)
	for {
		n, clientAddr, err := listenConn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			slog.Error("udp read error", "port", port, "error", err)
			break
		}
		clientKey := clientAddr.String()
		clientIP, _, _ := net.SplitHostPort(clientKey)

		mu.Lock()
		sess := sessions[clientKey]
		mu.Unlock()

		if sess == nil {
			// First packet of a new flow — authorise the source IP once. For raw
			// UDP there is no cookie/HTTP login, so IP session auth (or the static
			// allowlist) is the only gate; this mirrors the TCP path.
			if IsBanned(clientIP) {
				RecordEvent(clientIP, routeUrl, OutcomeBanned)
				slog.Warn("udp: packet dropped, banned", "client", clientIP, "route", routeUrl)
				continue
			}
			authorized, accessUser := authorizeIP(routeUrl, clientIP)
			if !authorized {
				logAccess(clientIP, "Unauthorized User", routeUrl)
				RecordEvent(clientIP, routeUrl, OutcomeTCPRejected)
				RecordFailure(clientIP)
				slog.Warn("udp: packet dropped, not authorized", "client", clientIP, "route", routeUrl)
				continue
			}
			targetConn, err := net.Dial("udp", target)
			if err != nil {
				RecordEvent(clientIP, routeUrl, OutcomeDialError)
				slog.Error("udp: failed to connect to target", "target", target, "client", clientIP, "error", err)
				continue
			}
			sess = &udpSession{targetConn: targetConn}
			sess.lastActive.Store(time.Now().UnixNano())
			mu.Lock()
			sessions[clientKey] = sess
			mu.Unlock()

			if accessUser != "" {
				SetTier(clientIP, storage.TierSignedIn)
			}
			RecordEvent(clientIP, routeUrl, OutcomeServed)
			logAccess(clientIP, accessUser, routeUrl)

			wg.Add(1)
			go func(s *udpSession, caddr net.Addr) {
				defer wg.Done()
				pumpUDPReplies(s, caddr, listenConn)
				s.targetConn.Close()
				mu.Lock()
				if sessions[caddr.String()] == s {
					delete(sessions, caddr.String())
				}
				mu.Unlock()
			}(sess, clientAddr)
		}

		sess.lastActive.Store(time.Now().UnixNano())
		if _, err := sess.targetConn.Write(buf[:n]); err != nil {
			slog.Debug("udp: target write failed", "client", clientIP, "error", err)
		}
	}

	// Shutdown: close every target socket so the reply pumps unblock and exit.
	mu.Lock()
	for _, s := range sessions {
		s.targetConn.Close()
	}
	mu.Unlock()
	wg.Wait()
	slog.Info("udp proxy stopped", "port", port)
}

// pumpUDPReplies copies datagrams from the target back to the client over the
// shared listen socket until the target socket is closed (idle reap or shutdown).
func pumpUDPReplies(s *udpSession, clientAddr net.Addr, listenConn net.PacketConn) {
	buf := make([]byte, udpBufSize)
	for {
		n, err := s.targetConn.Read(buf)
		if err != nil {
			return
		}
		s.lastActive.Store(time.Now().UnixNano())
		if _, err := listenConn.WriteTo(buf[:n], clientAddr); err != nil {
			return
		}
	}
}

// reapUDPSessions closes sessions idle for longer than udpSessionTimeout.
// Closing the target socket unblocks the session's reply pump, which then
// removes it from the map.
func reapUDPSessions(ctx context.Context, mu *sync.Mutex, sessions map[string]*udpSession) {
	t := time.NewTicker(udpSessionTimeout / 2)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cutoff := time.Now().Add(-udpSessionTimeout).UnixNano()
			mu.Lock()
			for k, s := range sessions {
				if s.lastActive.Load() < cutoff {
					s.targetConn.Close()
					delete(sessions, k)
				}
			}
			mu.Unlock()
		}
	}
}
