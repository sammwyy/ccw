package modules

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	socketio "github.com/googollee/go-socket.io"
)

type NetworkModule struct {
	server    *socketio.Server
	monitors  map[string]*PortMonitor
	monitorMu sync.RWMutex
}

type DownloadRequest struct {
	URL  string `json:"url" binding:"required"`
	Path string `json:"path" binding:"required"`
}

type NetworkOperation struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type PortMonitor struct {
	conn     socketio.Conn
	protocol string
	iface    string
	interval int
	previous map[int]bool
	stop     chan bool
	running  bool
	mu       sync.RWMutex
}

type PortChange struct {
	Port      int    `json:"port"`
	Status    string `json:"status"` // "opened" or "closed"
	Protocol  string `json:"protocol"`
	Interface string `json:"interface"`
	Timestamp int64  `json:"timestamp"`
}

func NewNetworkModule(server *socketio.Server) *NetworkModule {
	return &NetworkModule{
		server:   server,
		monitors: make(map[string]*PortMonitor),
	}
}

// REST API Handlers

// DownloadFile downloads a file from URL to specified path
func (nm *NetworkModule) DownloadFile(c *gin.Context) {
	var req DownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, NetworkOperation{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(req.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, NetworkOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to create directory: %v", err),
		})
		return
	}

	// Download the file
	resp, err := http.Get(req.URL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, NetworkOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to download file: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, NetworkOperation{
			Success: false,
			Message: fmt.Sprintf("HTTP error: %s", resp.Status),
		})
		return
	}

	// Create the destination file
	file, err := os.Create(req.Path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, NetworkOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to create file: %v", err),
		})
		return
	}
	defer file.Close()

	// Copy the content
	bytesWritten, err := io.Copy(file, resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, NetworkOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to write file: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, NetworkOperation{
		Success: true,
		Message: "File downloaded successfully",
		Data: map[string]interface{}{
			"bytes_written": bytesWritten,
			"content_type":  resp.Header.Get("Content-Type"),
			"file_path":     req.Path,
		},
	})
}

// GetCurrentPorts returns currently listening ports
func (nm *NetworkModule) GetCurrentPorts(c *gin.Context) {
	protocol := c.DefaultQuery("protocol", "tcp")
	iface := c.DefaultQuery("interface", "127.0.0.1")

	var protocols []string
	switch protocol {
	case "tcp":
		protocols = []string{"tcp"}
	case "udp":
		protocols = []string{"udp"}
	case "both":
		protocols = []string{"tcp", "udp"}
	default:
		c.JSON(http.StatusBadRequest, NetworkOperation{
			Success: false,
			Message: "Invalid protocol. Use 'tcp', 'udp', or 'both'",
		})
		return
	}

	ports := nm.getListeningPorts(protocols, iface)

	var portList []int
	for port := range ports {
		portList = append(portList, port)
	}

	c.JSON(http.StatusOK, NetworkOperation{
		Success: true,
		Message: "Current listening ports retrieved",
		Data: map[string]interface{}{
			"ports":     portList,
			"protocol":  protocol,
			"interface": iface,
			"count":     len(portList),
		},
	})
}

// Socket.IO Handlers

// StartPortMonitoring starts monitoring port changes for a connection
func (nm *NetworkModule) StartPortMonitoring(conn socketio.Conn, protocol, iface string, interval int) {
	monitorID := fmt.Sprintf("%s_%s_%s", conn.ID(), protocol, iface)

	nm.monitorMu.Lock()
	defer nm.monitorMu.Unlock()

	// Stop existing monitor if any
	if existingMonitor, exists := nm.monitors[monitorID]; exists {
		existingMonitor.Stop()
	}

	// Validate parameters
	var protocols []string
	switch protocol {
	case "tcp":
		protocols = []string{"tcp"}
	case "udp":
		protocols = []string{"udp"}
	case "both":
		protocols = []string{"tcp", "udp"}
	default:
		conn.Emit("net:error", map[string]interface{}{
			"message": "Invalid protocol. Use 'tcp', 'udp', or 'both'",
		})
		return
	}

	if interval < 1 {
		interval = 2 // Default to 2 seconds
	}

	// Create new monitor
	monitor := &PortMonitor{
		conn:     conn,
		protocol: protocol,
		iface:    iface,
		interval: interval,
		stop:     make(chan bool, 1),
		running:  true,
		previous: nm.getListeningPorts(protocols, iface),
	}

	nm.monitors[monitorID] = monitor

	// Start monitoring in goroutine
	go nm.runPortMonitor(monitor, protocols)

	conn.Emit("net:monitor:started", map[string]interface{}{
		"protocol":  protocol,
		"interface": iface,
		"interval":  interval,
		"timestamp": time.Now().Unix(),
	})
}

// StopPortMonitoring stops monitoring for a connection
func (nm *NetworkModule) StopPortMonitoring(conn socketio.Conn, protocol, iface string) {
	monitorID := fmt.Sprintf("%s_%s_%s", conn.ID(), protocol, iface)

	nm.monitorMu.Lock()
	defer nm.monitorMu.Unlock()

	if monitor, exists := nm.monitors[monitorID]; exists {
		monitor.Stop()
		delete(nm.monitors, monitorID)

		conn.Emit("net:monitor:stopped", map[string]interface{}{
			"protocol":  protocol,
			"interface": iface,
			"timestamp": time.Now().Unix(),
		})
	}
}

// CleanupConnection cleans up all monitors for a disconnected connection
func (nm *NetworkModule) CleanupConnection(connectionID string) {
	nm.monitorMu.Lock()
	defer nm.monitorMu.Unlock()

	toDelete := []string{}
	for monitorID, monitor := range nm.monitors {
		if strings.HasPrefix(monitorID, connectionID+"_") {
			monitor.Stop()
			toDelete = append(toDelete, monitorID)
		}
	}

	for _, id := range toDelete {
		delete(nm.monitors, id)
	}
}

// Helper functions

func (pm *PortMonitor) Stop() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.running {
		pm.running = false
		close(pm.stop)
	}
}

func (nm *NetworkModule) runPortMonitor(monitor *PortMonitor, protocols []string) {
	ticker := time.NewTicker(time.Duration(monitor.interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-monitor.stop:
			return
		case <-ticker.C:
			monitor.mu.RLock()
			if !monitor.running {
				monitor.mu.RUnlock()
				return
			}
			monitor.mu.RUnlock()

			current := nm.getListeningPorts(protocols, monitor.iface)
			opened, closed := nm.diffPorts(monitor.previous, current)

			if len(opened) > 0 || len(closed) > 0 {
				changes := []PortChange{}
				timestamp := time.Now().Unix()

				for _, port := range opened {
					changes = append(changes, PortChange{
						Port:      port,
						Status:    "opened",
						Protocol:  monitor.protocol,
						Interface: monitor.iface,
						Timestamp: timestamp,
					})
				}

				for _, port := range closed {
					changes = append(changes, PortChange{
						Port:      port,
						Status:    "closed",
						Protocol:  monitor.protocol,
						Interface: monitor.iface,
						Timestamp: timestamp,
					})
				}

				monitor.conn.Emit("net:port:changes", map[string]interface{}{
					"changes":   changes,
					"timestamp": timestamp,
				})
			}

			monitor.previous = current
		}
	}
}

func (nm *NetworkModule) parsePortsFile(file string, iface string) map[int]bool {
	ports := make(map[int]bool)
	f, err := os.Open(file)
	if err != nil {
		return ports
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Scan() // skip header

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		localAddress := fields[1]
		ipPort := strings.Split(localAddress, ":")
		if len(ipPort) != 2 {
			continue
		}

		ipHex := ipPort[0]
		portHex := ipPort[1]
		ip := nm.parseHexIP(ipHex)

		if iface != "any" && iface != ip {
			continue
		}

		port, err := strconv.ParseInt(portHex, 16, 32)
		if err == nil {
			ports[int(port)] = true
		}
	}

	return ports
}

func (nm *NetworkModule) parseHexIP(hexIP string) string {
	if len(hexIP) != 8 {
		return ""
	}

	ipBytes := []string{
		hexIP[6:8],
		hexIP[4:6],
		hexIP[2:4],
		hexIP[0:2],
	}

	parts := make([]string, 4)
	for i, b := range ipBytes {
		val, _ := strconv.ParseUint(b, 16, 8)
		parts[i] = fmt.Sprintf("%d", val)
	}

	return strings.Join(parts, ".")
}

func (nm *NetworkModule) getListeningPorts(protocols []string, iface string) map[int]bool {
	files := map[string]string{
		"tcp": "/proc/net/tcp",
		"udp": "/proc/net/udp",
	}

	ports := make(map[int]bool)
	for _, proto := range protocols {
		path, ok := files[proto]
		if !ok {
			continue
		}
		for port := range nm.parsePortsFile(path, iface) {
			ports[port] = true
		}
	}

	return ports
}

func (nm *NetworkModule) diffPorts(old, current map[int]bool) (opened, closed []int) {
	for port := range current {
		if !old[port] {
			opened = append(opened, port)
		}
	}

	for port := range old {
		if !current[port] {
			closed = append(closed, port)
		}
	}

	return
}
