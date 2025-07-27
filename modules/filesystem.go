package modules

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gin-gonic/gin"
	socketio "github.com/googollee/go-socket.io"
)

type FileSystemModule struct {
	server   *socketio.Server
	watchers map[string]*fsnotify.Watcher
	clients  map[string]map[string]bool // clientID -> paths being watched
	mutex    sync.RWMutex
}

type FileInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime time.Time `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
}

type FileOperation struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func NewFileSystemModule(server *socketio.Server) *FileSystemModule {
	return &FileSystemModule{
		server:   server,
		watchers: make(map[string]*fsnotify.Watcher),
		clients:  make(map[string]map[string]bool),
	}
}

// REST API Handlers

// ListDirectory lists files and directories in the specified path
func (fsm *FileSystemModule) ListDirectory(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: "path parameter is required",
		})
		return
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to read directory: %v", err),
		})
		return
	}

	var files []FileInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(files, FileInfo{
			Name:    entry.Name(),
			Path:    filepath.Join(path, entry.Name()),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime(),
			IsDir:   entry.IsDir(),
		})
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "Directory listed successfully",
		Data:    files,
	})
}

// CreateFile creates a new file
func (fsm *FileSystemModule) CreateFile(c *gin.Context) {
	var req struct {
		Path    string `json:"path" binding:"required"`
		Content string `json:"content"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(req.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to create directory: %v", err),
		})
		return
	}

	file, err := os.Create(req.Path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to create file: %v", err),
		})
		return
	}
	defer file.Close()

	if req.Content != "" {
		if _, err := file.WriteString(req.Content); err != nil {
			c.JSON(http.StatusInternalServerError, FileOperation{
				Success: false,
				Message: fmt.Sprintf("Failed to write content: %v", err),
			})
			return
		}
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "File created successfully",
	})
}

// DeleteFile deletes a file or directory
func (fsm *FileSystemModule) DeleteFile(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: "path parameter is required",
		})
		return
	}

	err := os.RemoveAll(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to delete: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "File/directory deleted successfully",
	})
}

// RenameFile renames a file or directory
func (fsm *FileSystemModule) RenameFile(c *gin.Context) {
	var req struct {
		OldPath string `json:"old_path" binding:"required"`
		NewPath string `json:"new_path" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	err := os.Rename(req.OldPath, req.NewPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to rename: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "File/directory renamed successfully",
	})
}

// CopyFile copies a file or directory
func (fsm *FileSystemModule) CopyFile(c *gin.Context) {
	var req struct {
		Source      string `json:"source" binding:"required"`
		Destination string `json:"destination" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	err := copyPath(req.Source, req.Destination)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to copy: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "File/directory copied successfully",
	})
}

// MoveFile moves a file or directory
func (fsm *FileSystemModule) MoveFile(c *gin.Context) {
	var req struct {
		Source      string `json:"source" binding:"required"`
		Destination string `json:"destination" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	// First copy, then delete source
	err := copyPath(req.Source, req.Destination)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to move (copy failed): %v", err),
		})
		return
	}

	err = os.RemoveAll(req.Source)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to move (delete source failed): %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "File/directory moved successfully",
	})
}

// ReadFile reads the content of a file
func (fsm *FileSystemModule) ReadFile(c *gin.Context) {
	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: "path parameter is required",
		})
		return
	}

	content, err := os.ReadFile(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to read file: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "File read successfully",
		Data:    string(content),
	})
}

// WriteFile writes content to a file
func (fsm *FileSystemModule) WriteFile(c *gin.Context) {
	var req struct {
		Path    string `json:"path" binding:"required"`
		Content string `json:"content" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	err := os.WriteFile(req.Path, []byte(req.Content), 0644)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to write file: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "File written successfully",
	})
}

// CreateDirectory creates a new directory
func (fsm *FileSystemModule) CreateDirectory(c *gin.Context) {
	var req struct {
		Path string `json:"path" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Invalid request: %v", err),
		})
		return
	}

	err := os.MkdirAll(req.Path, 0755)
	if err != nil {
		c.JSON(http.StatusInternalServerError, FileOperation{
			Success: false,
			Message: fmt.Sprintf("Failed to create directory: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, FileOperation{
		Success: true,
		Message: "Directory created successfully",
	})
}

// Socket.IO Handlers

// WatchFiles starts watching a directory for file changes
func (fsm *FileSystemModule) WatchFiles(conn socketio.Conn, path string) {
	fsm.mutex.Lock()
	defer fsm.mutex.Unlock()

	clientID := conn.ID()

	// Initialize client map if not exists
	if fsm.clients[clientID] == nil {
		fsm.clients[clientID] = make(map[string]bool)
	}

	// Check if already watching this path for this client
	if fsm.clients[clientID][path] {
		conn.Emit("fs:error", map[string]interface{}{
			"message": "Already watching this path",
			"path":    path,
		})
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		conn.Emit("fs:error", map[string]interface{}{
			"message": fmt.Sprintf("Failed to create watcher: %v", err),
			"path":    path,
		})
		return
	}

	// Watch the directory recursively
	err = filepath.WalkDir(path, func(walkPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return watcher.Add(walkPath)
		}
		return nil
	})

	if err != nil {
		watcher.Close()
		conn.Emit("fs:error", map[string]interface{}{
			"message": fmt.Sprintf("Failed to watch path: %v", err),
			"path":    path,
		})
		return
	}

	watcherKey := fmt.Sprintf("%s:%s", clientID, path)
	fsm.watchers[watcherKey] = watcher
	fsm.clients[clientID][path] = true

	// Start watching in a goroutine
	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				eventData := map[string]interface{}{
					"path":      event.Name,
					"operation": event.Op.String(),
					"timestamp": time.Now(),
				}

				conn.Emit("fs:change", eventData)

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				conn.Emit("fs:error", map[string]interface{}{
					"message": fmt.Sprintf("Watcher error: %v", err),
					"path":    path,
				})
			}
		}
	}()

	conn.Emit("fs:watching", map[string]interface{}{
		"message": "Started watching directory",
		"path":    path,
	})
}

// UnwatchFiles stops watching a directory
func (fsm *FileSystemModule) UnwatchFiles(conn socketio.Conn, path string) {
	fsm.mutex.Lock()
	defer fsm.mutex.Unlock()

	clientID := conn.ID()
	watcherKey := fmt.Sprintf("%s:%s", clientID, path)

	if watcher, exists := fsm.watchers[watcherKey]; exists {
		watcher.Close()
		delete(fsm.watchers, watcherKey)

		if fsm.clients[clientID] != nil {
			delete(fsm.clients[clientID], path)
		}

		conn.Emit("fs:unwatched", map[string]interface{}{
			"message": "Stopped watching directory",
			"path":    path,
		})
	} else {
		conn.Emit("fs:error", map[string]interface{}{
			"message": "Path not being watched",
			"path":    path,
		})
	}
}

// CleanupConnection cleans up resources when a client disconnects
func (fsm *FileSystemModule) CleanupConnection(clientID string) {
	fsm.mutex.Lock()
	defer fsm.mutex.Unlock()

	if paths, exists := fsm.clients[clientID]; exists {
		for path := range paths {
			watcherKey := fmt.Sprintf("%s:%s", clientID, path)
			if watcher, exists := fsm.watchers[watcherKey]; exists {
				watcher.Close()
				delete(fsm.watchers, watcherKey)
			}
		}
		delete(fsm.clients, clientID)
	}
}

// Helper function to copy files and directories recursively
func copyPath(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if srcInfo.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	// Create destination directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return err
	}

	// Copy file permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}

func copyDir(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}
