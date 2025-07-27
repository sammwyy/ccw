package modules

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	socketio "github.com/googollee/go-socket.io"
)

type ShellModule struct {
	server   *socketio.Server
	sessions map[string]*ShellSession
	clients  map[string][]string // clientID -> sessionIDs
	mutex    sync.RWMutex
}

type ShellSession struct {
	ID       string
	ClientID string
	Command  *exec.Cmd
	PTY      *os.File
	Input    io.WriteCloser
	Output   io.ReadCloser
	Done     chan bool
	Active   bool
}

type CommandRequest struct {
	Command string            `json:"command" binding:"required"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	WorkDir string            `json:"workdir"`
	Timeout int               `json:"timeout"` // in seconds
}

type ShellOperation struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type CommandResult struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Duration   string `json:"duration"`
	Terminated bool   `json:"terminated"`
}

func NewShellModule(server *socketio.Server) *ShellModule {
	return &ShellModule{
		server:   server,
		sessions: make(map[string]*ShellSession),
		clients:  make(map[string][]string),
	}
}

// REST API Handlers

// ExecuteCommand executes a command and returns the output
func (sm *ShellModule) ExecuteCommand(c *gin.Context) {
	var req CommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ShellOperation{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	startTime := time.Now()

	// Create command
	var cmd *exec.Cmd
	if len(req.Args) > 0 {
		cmd = exec.Command(req.Command, req.Args...)
	} else {
		cmd = exec.Command("sh", "-c", req.Command)
	}

	// Set working directory if specified
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	// Set environment variables
	if req.Env != nil {
		env := os.Environ()
		for key, value := range req.Env {
			env = append(env, fmt.Sprintf("%s=%s", key, value))
		}
		cmd.Env = env
	}

	// Setup timeout if specified
	if req.Timeout > 0 {
		go func() {
			time.Sleep(time.Duration(req.Timeout) * time.Second)
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		}()
	}

	// Execute command
	stdout, stderr, exitCode, terminated := sm.executeCommand(cmd)
	duration := time.Since(startTime)

	result := CommandResult{
		Command:    req.Command,
		ExitCode:   exitCode,
		Stdout:     stdout,
		Stderr:     stderr,
		Duration:   duration.String(),
		Terminated: terminated,
	}

	c.JSON(http.StatusOK, ShellOperation{
		Success: true,
		Message: "Command executed",
		Data:    result,
	})
}

// Socket.IO Handlers

// SpawnInteractiveShell spawns an interactive shell session
func (sm *ShellModule) SpawnInteractiveShell(conn socketio.Conn, command string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	clientID := conn.ID()
	sessionID := uuid.New().String()

	// Default to bash if no command specified
	if command == "" {
		command = "/bin/bash"
	}

	// Create command
	cmd := exec.Command(command)
	cmd.Env = os.Environ()

	// Start the command with a PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		conn.Emit("shell:error", map[string]interface{}{
			"message": fmt.Sprintf("Failed to start shell: %v", err),
		})
		return
	}

	// Create session
	session := &ShellSession{
		ID:       sessionID,
		ClientID: clientID,
		Command:  cmd,
		PTY:      ptmx,
		Done:     make(chan bool),
		Active:   true,
	}

	// Store session
	sm.sessions[sessionID] = session
	if sm.clients[clientID] == nil {
		sm.clients[clientID] = make([]string, 0)
	}
	sm.clients[clientID] = append(sm.clients[clientID], sessionID)

	// Start reading output in a goroutine
	go func() {
		defer func() {
			sm.mutex.Lock()
			session.Active = false
			close(session.Done)
			ptmx.Close()
			sm.mutex.Unlock()
		}()

		scanner := bufio.NewScanner(ptmx)
		for scanner.Scan() {
			line := scanner.Text()
			conn.Emit("shell:output", map[string]interface{}{
				"session_id": sessionID,
				"data":       line + "\n",
				"type":       "stdout",
				"timestamp":  time.Now(),
			})
		}

		// Check if command finished
		if err := cmd.Wait(); err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				conn.Emit("shell:exit", map[string]interface{}{
					"session_id": sessionID,
					"exit_code":  exitError.ExitCode(),
					"timestamp":  time.Now(),
				})
			}
		} else {
			conn.Emit("shell:exit", map[string]interface{}{
				"session_id": sessionID,
				"exit_code":  0,
				"timestamp":  time.Now(),
			})
		}
	}()

	conn.Emit("shell:spawned", map[string]interface{}{
		"session_id": sessionID,
		"command":    command,
		"timestamp":  time.Now(),
	})
}

// SendInput sends input to an interactive shell session
func (sm *ShellModule) SendInput(conn socketio.Conn, sessionID, input string) {
	sm.mutex.RLock()
	session, exists := sm.sessions[sessionID]
	sm.mutex.RUnlock()

	if !exists {
		conn.Emit("shell:error", map[string]interface{}{
			"message":    "Session not found",
			"session_id": sessionID,
		})
		return
	}

	// Verify client owns this session
	if session.ClientID != conn.ID() {
		conn.Emit("shell:error", map[string]interface{}{
			"message":    "Access denied",
			"session_id": sessionID,
		})
		return
	}

	if !session.Active {
		conn.Emit("shell:error", map[string]interface{}{
			"message":    "Session is not active",
			"session_id": sessionID,
		})
		return
	}

	// Send input to PTY
	_, err := session.PTY.Write([]byte(input))
	if err != nil {
		conn.Emit("shell:error", map[string]interface{}{
			"message":    fmt.Sprintf("Failed to send input: %v", err),
			"session_id": sessionID,
		})
		return
	}
}

// KillSession terminates a shell session
func (sm *ShellModule) KillSession(conn socketio.Conn, sessionID string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		conn.Emit("shell:error", map[string]interface{}{
			"message":    "Session not found",
			"session_id": sessionID,
		})
		return
	}

	// Verify client owns this session
	if session.ClientID != conn.ID() {
		conn.Emit("shell:error", map[string]interface{}{
			"message":    "Access denied",
			"session_id": sessionID,
		})
		return
	}

	// Kill the process
	if session.Command.Process != nil {
		err := session.Command.Process.Signal(syscall.SIGTERM)
		if err != nil {
			// If SIGTERM fails, try SIGKILL
			session.Command.Process.Kill()
		}
	}

	// Clean up session
	session.Active = false
	delete(sm.sessions, sessionID)

	// Remove from client sessions
	if clientSessions, exists := sm.clients[conn.ID()]; exists {
		for i, id := range clientSessions {
			if id == sessionID {
				sm.clients[conn.ID()] = append(clientSessions[:i], clientSessions[i+1:]...)
				break
			}
		}
	}

	conn.Emit("shell:killed", map[string]interface{}{
		"session_id": sessionID,
		"timestamp":  time.Now(),
	})
}

// ListSessions lists all active sessions for a client
func (sm *ShellModule) ListSessions(conn socketio.Conn) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	clientID := conn.ID()
	sessionIDs, exists := sm.clients[clientID]
	if !exists {
		sessionIDs = []string{}
	}

	var sessions []map[string]interface{}
	for _, sessionID := range sessionIDs {
		if session, exists := sm.sessions[sessionID]; exists && session.Active {
			sessions = append(sessions, map[string]interface{}{
				"session_id": sessionID,
				"active":     session.Active,
				"command":    session.Command.Args[0],
			})
		}
	}

	conn.Emit("shell:sessions", map[string]interface{}{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

// CleanupConnection cleans up all sessions for a disconnected client
func (sm *ShellModule) CleanupConnection(clientID string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if sessionIDs, exists := sm.clients[clientID]; exists {
		for _, sessionID := range sessionIDs {
			if session, exists := sm.sessions[sessionID]; exists {
				// Kill the process
				if session.Command.Process != nil {
					session.Command.Process.Kill()
				}
				session.Active = false
				delete(sm.sessions, sessionID)
			}
		}
		delete(sm.clients, clientID)
	}
}

// Helper functions

// executeCommand executes a command and captures output
func (sm *ShellModule) executeCommand(cmd *exec.Cmd) (stdout, stderr string, exitCode int, terminated bool) {
	var stdoutBuf, stderrBuf []byte
	var err error

	// Capture stdout and stderr
	cmd.Stdout = &stdoutCapture{&stdoutBuf}
	cmd.Stderr = &stderrCapture{&stderrBuf}

	// Start command
	err = cmd.Start()
	if err != nil {
		return "", fmt.Sprintf("Failed to start command: %v", err), -1, false
	}

	// Wait for command to finish
	err = cmd.Wait()

	stdout = string(stdoutBuf)
	stderr = string(stderrBuf)

	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
			terminated = true
		}
	} else {
		exitCode = 0
	}

	return stdout, stderr, exitCode, terminated
}

// Custom writers to capture command output
type stdoutCapture struct {
	data *[]byte
}

func (sc *stdoutCapture) Write(p []byte) (n int, err error) {
	*sc.data = append(*sc.data, p...)
	return len(p), nil
}

type stderrCapture struct {
	data *[]byte
}

func (sc *stderrCapture) Write(p []byte) (n int, err error) {
	*sc.data = append(*sc.data, p...)
	return len(p), nil
}
