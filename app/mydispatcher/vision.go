package mydispatcher

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/features/stats"
)

// VisionStatsManager manages statistics for Vision connections
type VisionStatsManager struct {
	mutex       sync.RWMutex
	visionConns map[string]*VisionConnection
	counters    map[string]*VisionCounter
}

// VisionConnection represents a Vision connection with its traffic statistics
type VisionConnection struct {
	UserTag       string
	StartTime     time.Time
	LastActive    time.Time
	TotalUpload   int64
	TotalDownload int64
	IsVision      bool
	IsActive      bool
}

// VisionCounter wraps the original counter with Vision-specific functionality
type VisionCounter struct {
	Original    stats.Counter
	VisionBytes int64
	mutex       sync.RWMutex
}

// NewVisionStatsManager creates a new Vision statistics manager
func NewVisionStatsManager() *VisionStatsManager {
	return &VisionStatsManager{
		visionConns: make(map[string]*VisionConnection),
		counters:    make(map[string]*VisionCounter),
	}
}

// IsVisionFlow checks if a connection is using Vision flow
func (vsm *VisionStatsManager) IsVisionFlow(ctx context.Context, dest xnet.Destination) bool {
	// Check if this is a Vision connection based on context
	if inbound := session.InboundFromContext(ctx); inbound != nil {
		if inbound.Tag != "" {
			// Check if this connection is using VLESS protocol
			if strings.Contains(inbound.Tag, "Vless") {
				// Check if Vision flow is enabled
				if user := inbound.User; user != nil && user.Email != "" {
					// Look for Vision flow indicators in the session
					if outbounds := session.OutboundsFromContext(ctx); len(outbounds) > 0 {
						outbound := outbounds[0]
						// Vision is typically used with TCP + TLS/Reality
						if dest.Network == xnet.Network_TCP &&
							strings.Contains(outbound.Target.String(), "reality") {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// TrackVisionConnection tracks a Vision connection for statistics
func (vsm *VisionStatsManager) TrackVisionConnection(userTag string, isVision bool) {
	vsm.mutex.Lock()
	defer vsm.mutex.Unlock()

	conn := &VisionConnection{
		UserTag:    userTag,
		StartTime:  time.Now(),
		LastActive: time.Now(),
		IsVision:   isVision,
		IsActive:   true,
	}

	vsm.visionConns[userTag] = conn
}

// UpdateVisionTraffic updates traffic statistics for a Vision connection
func (vsm *VisionStatsManager) UpdateVisionTraffic(userTag string, upload, download int64) {
	vsm.mutex.Lock()
	defer vsm.mutex.Unlock()

	if conn, exists := vsm.visionConns[userTag]; exists {
		conn.TotalUpload += upload
		conn.TotalDownload += download
		conn.LastActive = time.Now()
	}
}

// GetVisionCounter gets or creates a Vision counter for a user
func (vsm *VisionStatsManager) GetVisionCounter(name string, original stats.Counter) *VisionCounter {
	vsm.mutex.Lock()
	defer vsm.mutex.Unlock()

	if counter, exists := vsm.counters[name]; exists {
		return counter
	}

	counter := &VisionCounter{
		Original: original,
	}
	vsm.counters[name] = counter
	return counter
}

// Add adds bytes to the Vision counter
func (vc *VisionCounter) Add(delta int64) int64 {
	vc.mutex.Lock()
	defer vc.mutex.Unlock()

	vc.VisionBytes += delta
	if vc.Original != nil {
		return vc.Original.Add(delta)
	}
	return vc.VisionBytes
}

// Value returns the total value including Vision bytes
func (vc *VisionCounter) Value() int64 {
	vc.mutex.RLock()
	defer vc.mutex.RUnlock()

	originalValue := int64(0)
	if vc.Original != nil {
		originalValue = vc.Original.Value()
	}

	return originalValue + vc.VisionBytes
}

// Set sets the counter value
func (vc *VisionCounter) Set(value int64) int64 {
	vc.mutex.Lock()
	defer vc.mutex.Unlock()

	if vc.Original != nil {
		result := vc.Original.Set(value)
		vc.VisionBytes = 0
		return result
	}
	vc.VisionBytes = value
	return value
}

// EnhancedSizeStatWriter is an enhanced version that handles Vision flows
type EnhancedSizeStatWriter struct {
	Counter       stats.Counter
	VisionCounter *VisionCounter
	Writer        buf.Writer
	UserTag       string
	IsVision      bool
	VisionManager *VisionStatsManager
	ctx           context.Context
}

// WriteMultiBuffer writes data and updates statistics appropriately
func (w *EnhancedSizeStatWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	size := int64(mb.Len())

	// Always update the counter
	if w.VisionCounter != nil {
		w.VisionCounter.Add(size)
	} else if w.Counter != nil {
		w.Counter.Add(size)
	}

	// If this is a Vision connection, also update Vision-specific stats
	if w.IsVision && w.VisionManager != nil && w.UserTag != "" {
		w.VisionManager.UpdateVisionTraffic(w.UserTag, size, 0)
	}

	return w.Writer.WriteMultiBuffer(mb)
}

// Close closes the writer
func (w *EnhancedSizeStatWriter) Close() error {
	return common.Close(w.Writer)
}

// Interrupt interrupts the writer
func (w *EnhancedSizeStatWriter) Interrupt() {
	common.Interrupt(w.Writer)
}

// EnhancedSizeStatReader is an enhanced version that handles Vision flows for reading
type EnhancedSizeStatReader struct {
	Counter       stats.Counter
	VisionCounter *VisionCounter
	Reader        buf.Reader
	UserTag       string
	IsVision      bool
	VisionManager *VisionStatsManager
	ctx           context.Context
}

// ReadMultiBuffer reads data and updates statistics appropriately
func (r *EnhancedSizeStatReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb, err := r.Reader.ReadMultiBuffer()
	if err != nil {
		return nil, err
	}

	size := int64(mb.Len())

	// Always update the counter
	if r.VisionCounter != nil {
		r.VisionCounter.Add(size)
	} else if r.Counter != nil {
		r.Counter.Add(size)
	}

	// If this is a Vision connection, also update Vision-specific stats
	if r.IsVision && r.VisionManager != nil && r.UserTag != "" {
		r.VisionManager.UpdateVisionTraffic(r.UserTag, 0, size)
	}

	return mb, nil
}

// Close closes the reader
func (r *EnhancedSizeStatReader) Close() error {
	return common.Close(r.Reader)
}

// Interrupt interrupts the reader
func (r *EnhancedSizeStatReader) Interrupt() {
	common.Interrupt(r.Reader)
}

// NetworkStatsCollector collects network-level statistics for Vision flows
type NetworkStatsCollector struct {
	mutex       sync.RWMutex
	connections map[string]*NetworkConnection
	manager     *VisionStatsManager
}

// NetworkConnection represents a network connection with its statistics
type NetworkConnection struct {
	UserTag      string
	LocalAddr    net.Addr
	RemoteAddr   net.Addr
	BytesRead    int64
	BytesWritten int64
	StartTime    time.Time
	LastActivity time.Time
	IsVision     bool
}

// NewNetworkStatsCollector creates a new network statistics collector
func NewNetworkStatsCollector(manager *VisionStatsManager) *NetworkStatsCollector {
	return &NetworkStatsCollector{
		connections: make(map[string]*NetworkConnection),
		manager:     manager,
	}
}

// TrackConnection tracks a network connection
func (nsc *NetworkStatsCollector) TrackConnection(userTag string, local, remote net.Addr, isVision bool) {
	nsc.mutex.Lock()
	defer nsc.mutex.Unlock()

	connKey := userTag + ":" + local.String() + "->" + remote.String()
	nsc.connections[connKey] = &NetworkConnection{
		UserTag:      userTag,
		LocalAddr:    local,
		RemoteAddr:   remote,
		StartTime:    time.Now(),
		LastActivity: time.Now(),
		IsVision:     isVision,
	}
}

// UpdateConnectionStats updates connection statistics
func (nsc *NetworkStatsCollector) UpdateConnectionStats(userTag string, local, remote net.Addr, bytesRead, bytesWritten int64) {
	nsc.mutex.Lock()
	defer nsc.mutex.Unlock()

	connKey := userTag + ":" + local.String() + "->" + remote.String()
	if conn, exists := nsc.connections[connKey]; exists {
		conn.BytesRead += bytesRead
		conn.BytesWritten += bytesWritten
		conn.LastActivity = time.Now()

		// Update Vision manager if this is a Vision connection
		if conn.IsVision && nsc.manager != nil {
			nsc.manager.UpdateVisionTraffic(userTag, bytesWritten, bytesRead)
		}
	}
}

// CleanupInactiveConnections removes inactive connections
func (nsc *NetworkStatsCollector) CleanupInactiveConnections(maxAge time.Duration) {
	nsc.mutex.Lock()
	defer nsc.mutex.Unlock()

	now := time.Now()
	for key, conn := range nsc.connections {
		if now.Sub(conn.LastActivity) > maxAge {
			delete(nsc.connections, key)
		}
	}
}
