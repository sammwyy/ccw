package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/googollee/go-socket.io/engineio"
	"github.com/googollee/go-socket.io/engineio/transport"
	"github.com/googollee/go-socket.io/engineio/transport/polling"
	"github.com/googollee/go-socket.io/engineio/transport/websocket"

	modules "github.com/sammwyy/ccw/modules"

	socketio "github.com/googollee/go-socket.io"
)

func main() {
	// Parse command line flags
	debug := flag.Bool("debug", false, "Enable debug mode")
	flag.Parse()

	// Set Gin mode based on debug flag
	if !*debug {
		gin.SetMode(gin.ReleaseMode)
	}

	// Get password from environment
	authToken := os.Getenv("AUTH_TOKEN")
	if authToken == "" {
		log.Fatal("AUTH_TOKEN environment variable is required")
	}

	// Initialize Gin router
	r := gin.Default()

	// Initialize Socket.IO server with authentication
	server := socketio.NewServer(&engineio.Options{
		Transports: []transport.Transport{
			&polling.Transport{
				CheckOrigin: func(r *http.Request) bool {
					return true
				},
			},
			&websocket.Transport{
				CheckOrigin: func(r *http.Request) bool {
					return true
				},
			},
		},
	})

	// Initialize modules
	fsModule := modules.NewFileSystemModule(server)
	netModule := modules.NewNetworkModule(server)
	shellModule := modules.NewShellModule(server)

	// Setup Socket.IO handlers
	setupSocketHandlers(server, fsModule, netModule, shellModule, authToken)

	// Setup REST API routes with authentication
	api := r.Group("/api")
	api.Use(authMiddleware(authToken))
	{
		// File system routes
		fs := api.Group("/fs")
		{
			fs.GET("/listdir", fsModule.ListDirectory)
			fs.POST("/create", fsModule.CreateFile)
			fs.DELETE("/delete", fsModule.DeleteFile)
			fs.PUT("/rename", fsModule.RenameFile)
			fs.POST("/copy", fsModule.CopyFile)
			fs.POST("/move", fsModule.MoveFile)
			fs.GET("/read", fsModule.ReadFile)
			fs.POST("/write", fsModule.WriteFile)
			fs.POST("/mkdir", fsModule.CreateDirectory)
		}

		// Network routes
		net := api.Group("/net")
		{
			net.POST("/download", netModule.DownloadFile)
			net.GET("/ports", netModule.GetCurrentPorts) // Reemplaza el scan de puertos
		}

		// Shell routes
		shell := api.Group("/shell")
		{
			shell.POST("/exec", shellModule.ExecuteCommand)
		}
	}

	// Socket.IO endpoint (no auth middleware here as it's handled in connection)
	r.GET("/socket.io/*any", gin.WrapH(server))
	r.POST("/socket.io/*any", gin.WrapH(server))

	// Health check endpoint (no authentication required)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// Get port from environment or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}

func setupSocketHandlers(server *socketio.Server, fs *modules.FileSystemModule, net *modules.NetworkModule, shell *modules.ShellModule, authToken string) {
	server.OnConnect("/", func(s socketio.Conn) error {
		// Check for authentication token in handshake query
		queryParams := strings.Split(s.URL().RawQuery, "&")
		authProvided := false

		for _, param := range queryParams {
			if after, ok := strings.CutPrefix(param, "auth="); ok {
				authValue := after
				if authValue == authToken {
					authProvided = true
					break
				}
			}
		}
		if !authProvided {
			log.Println("Unauthorized connection attempt from:", s.RemoteAddr())
			s.Close()
			return nil
		}

		// Set context for the connection
		s.SetContext("")
		log.Println("Client connected:", s.ID())
		return nil
	})

	// File system handlers
	server.OnEvent("/", "fs:watch", func(s socketio.Conn, path string) {
		log.Printf("Starting file watch for path: %s", path)
		fs.WatchFiles(s, path)
	})

	server.OnEvent("/", "fs:unwatch", func(s socketio.Conn, path string) {
		log.Printf("Stopping file watch for path: %s", path)
		fs.UnwatchFiles(s, path)
	})

	// Network handlers
	server.OnEvent("/", "net:monitor:start", func(s socketio.Conn, protocol, iface string, interval int) {
		log.Printf("Starting port monitoring for %s on %s (interval: %ds)", protocol, iface, interval)
		net.StartPortMonitoring(s, protocol, iface, interval)
	})

	server.OnEvent("/", "net:monitor:stop", func(s socketio.Conn, protocol, iface string) {
		log.Printf("Stopping port monitoring for %s on %s", protocol, iface)
		net.StopPortMonitoring(s, protocol, iface)
	})

	// Shell handlers
	server.OnEvent("/", "shell:spawn", func(s socketio.Conn, command string) {
		log.Printf("Spawning interactive shell: %s", command)
		shell.SpawnInteractiveShell(s, command)
	})

	server.OnEvent("/", "shell:input", func(s socketio.Conn, sessionID, input string) {
		shell.SendInput(s, sessionID, input)
	})

	server.OnEvent("/", "shell:kill", func(s socketio.Conn, sessionID string) {
		shell.KillSession(s, sessionID)
	})

	server.OnDisconnect("/", func(s socketio.Conn, reason string) {
		log.Printf("Client disconnected: %s, reason: %s", s.ID(), reason)
		// Cleanup resources for this connection
		fs.CleanupConnection(s.ID())
		net.CleanupConnection(s.ID())
		shell.CleanupConnection(s.ID())
	})

	go func() {
		if err := server.Serve(); err != nil {
			log.Fatalf("Socket.IO server error: %v", err)
		}
	}()
}

func authMiddleware(password string) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader != "Bearer "+password {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
		log.Println("Authenticated request:", c.Request.Method, c.Request.URL.Path)
		log.Println("Client IP:", c.ClientIP())
		log.Println("User-Agent:", c.Request.UserAgent())
	}
}
