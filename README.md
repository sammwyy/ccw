# CCW: Container Control Worker

A comprehensive Go-based worker application for Docker containers that provides HTTP REST API and Socket.IO capabilities for file system operations, network utilities, and shell command execution.

## Features

### File System Module (`/api/fs`)
- **List Directory**: Get files and directories in a path
- **Create File**: Create new files with content
- **Delete**: Remove files or directories
- **Rename**: Rename files or directories
- **Copy**: Copy files or directories
- **Move**: Move files or directories
- **Read File**: Read file contents
- **Write File**: Write content to files
- **Create Directory**: Create new directories
- **Real-time File Watching**: Monitor file changes via Socket.IO

### Network Module (`/api/net`)
- **Download Files**: Download files from URLs to specified paths
- **Port Monitoring**: Real-time monitoring of listening ports with change detection
- **Current Port Status**: Get currently listening ports for TCP/UDP protocols

### Shell Module (`/api/shell`)
- **Command Execution**: Execute shell commands with output capture
- **Interactive Shells**: Spawn interactive shell sessions via Socket.IO
- **Real-time I/O**: Send input and receive output in real-time
- **Session Management**: Manage multiple concurrent shell sessions

### Security Features
- **Bearer Token Authentication**: All API endpoints and Socket.IO connections require authentication
- **Environment-based Configuration**: Auth Token and other settings configurable via environment variables
- **Debug Mode Control**: Production-ready logging controls

## Installation

### Using Docker (Recommended)

1. Build the Docker image:
```bash
docker build -t ccw .
```

2. Run the container with authentication:
```bash
# Production mode
docker run -d -p 8080:8080 -e AUTH_TOKEN="your-secure-token" --name worker ccw

# Debug mode
docker run -d -p 8080:8080 -e AUTH_TOKEN="your-secure-token" --name worker ccw --debug
```

### Local Development

1. Install dependencies:
```bash
go mod tidy
```

2. Set environment variables and run:
```bash
# Set required token
export AUTH_TOKEN="your-secure-token"

# Production mode (default)
go run *.go

# Debug mode
go run *.go --debug
```

## Authentication

All API endpoints (except `/health`) and Socket.IO connections require authentication using Bearer tokens.

### Environment Variables
- `AUTH_TOKEN`: **Required**. The token used for Bearer token authentication

### REST API Authentication

Include the Bearer token in the Authorization header:

```bash
curl -H "Authorization: Bearer your-secure-token" \
     http://localhost:8080/api/fs/listdir?path=/home
```

### Socket.IO Authentication

Include the token as a query parameter when connecting:

```javascript
const socket = io('http://localhost:8080', {
  query: {
    token: 'your-secure-token'
  }
});
```

**Authentication Responses:**
- **401 Unauthorized**: Missing or invalid token
- **403 Forbidden**: Token format incorrect (should be `Bearer <token>`)

## Configuration

### Command Line Arguments

- `--debug`: Enable debug mode with verbose logging (default: production mode)

### Environment Variables

- `AUTH_TOKEN`: **Required**. Authentication token for API access
- `PORT`: Server port (default: 8080)

### Debug vs Production Mode

**Production Mode (default)**:
- Minimal logging output
- Optimized performance
- Set automatically when `--debug` flag is not present

**Debug Mode** (`--debug` flag):
- Verbose request/response logging
- Detailed error information
- Development-friendly output

## API Documentation

### Authentication Required

All endpoints below require the `Authorization: Bearer <token>` header unless otherwise specified.

### File System Endpoints

#### `GET /api/fs/listdir`
List files and directories in a path.
- **Query Parameters**: `path` (required)
- **Example**: 
```bash
curl -H "Authorization: Bearer your-secure-token" \
     "http://localhost:8080/api/fs/listdir?path=/home/user"
```

#### `POST /api/fs/create`
Create a new file.
```bash
curl -X POST http://localhost:8080/api/fs/create \
  -H "Authorization: Bearer your-secure-token" \
  -H "Content-Type: application/json" \
  -d '{"path":"/path/to/file.txt","content":"file content"}'
```

#### `DELETE /api/fs/delete`
Delete a file or directory.
- **Query Parameters**: `path` (required)

#### `PUT /api/fs/rename`
Rename a file or directory.
```json
{
  "old_path": "/old/path",
  "new_path": "/new/path"
}
```

#### `POST /api/fs/copy`
Copy a file or directory.
```json
{
  "source": "/source/path",
  "destination": "/destination/path"
}
```

#### `POST /api/fs/move`
Move a file or directory.
```json
{
  "source": "/source/path",
  "destination": "/destination/path"
}
```

#### `GET /api/fs/read`
Read file contents.
- **Query Parameters**: `path` (required)

#### `POST /api/fs/write`
Write content to a file.
```json
{
  "path": "/path/to/file.txt",
  "content": "new content"
}
```

#### `POST /api/fs/mkdir`
Create a directory.
```json
{
  "path": "/path/to/directory"
}
```

### Network Endpoints

#### `POST /api/net/download`
Download a file from URL.
```bash
curl -X POST http://localhost:8080/api/net/download \
  -H "Authorization: Bearer your-secure-token" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/file.zip","path":"/local/path/file.zip"}'
```

#### `GET /api/net/ports`
Get currently listening ports on the system.
- **Query Parameters**: 
  - `protocol` (optional): `tcp`, `udp`, or `both` (default: `tcp`)
  - `interface` (optional): IP address to filter by, or `any` for all interfaces (default: `127.0.0.1`)
- **Example**: 
```bash
curl -H "Authorization: Bearer your-secure-token" \
     "http://localhost:8080/api/net/ports?protocol=both&interface=any"
```

**Response Example**:
```json
{
  "success": true,
  "message": "Current listening ports retrieved",
  "data": {
    "ports": [22, 80, 443, 8080],
    "protocol": "both",
    "interface": "any",
    "count": 4
  }
}
```

### Shell Endpoints

#### `POST /api/shell/exec`
Execute a shell command.
```bash
curl -X POST http://localhost:8080/api/shell/exec \
  -H "Authorization: Bearer your-secure-token" \
  -H "Content-Type: application/json" \
  -d '{"command":"ls -la","args":["-la"],"env":{"VAR":"value"},"workdir":"/home/user","timeout":30}'
```

### Health Check Endpoint

#### `GET /health`
Health check endpoint (no authentication required).
```bash
curl http://localhost:8080/health
```

## Socket.IO Events

### Authentication

Socket.IO connections must include the authentication token:

```javascript
const socket = io('http://localhost:8080', {
  query: {
    token: 'your-secure-token'
  }
});

// Handle authentication errors
socket.on('connect_error', (error) => {
  console.error('Connection failed:', error.message);
});
```

### File System Events

#### Client to Server
- `fs:watch` - Start watching a directory for changes
  - **Data**: `"/path/to/watch"`
- `fs:unwatch` - Stop watching a directory
  - **Data**: `"/path/to/unwatch"`

#### Server to Client
- `fs:change` - File system change detected
- `fs:watching` - Confirmation that watching started
- `fs:unwatched` - Confirmation that watching stopped
- `fs:error` - File system operation error

### Network Events

#### Client to Server
- `net:monitor:start` - Start real-time port monitoring
  - **Data**: `protocol, interface, interval`
  - **Example**: `socket.emit('net:monitor:start', 'both', '127.0.0.1', 2)`
- `net:monitor:stop` - Stop port monitoring
  - **Data**: `protocol, interface`
  - **Example**: `socket.emit('net:monitor:stop', 'both', '127.0.0.1')`

#### Server to Client
- `net:monitor:started` - Port monitoring started
  - **Data**: 
    ```json
    {
      "protocol": "both",
      "interface": "127.0.0.1",
      "interval": 2,
      "timestamp": 1640995200
    }
    ```
- `net:monitor:stopped` - Port monitoring stopped
- `net:port:changes` - Port status changes detected
  - **Data**:
    ```json
    {
      "changes": [
        {
          "port": 8080,
          "status": "opened",
          "protocol": "tcp",
          "interface": "127.0.0.1",
          "timestamp": 1640995200
        }
      ],
      "timestamp": 1640995200
    }
    ```
- `net:error` - Network operation error

### Shell Events

#### Client to Server
- `shell:spawn` - Spawn interactive shell
  - **Data**: `"/bin/bash"` (command)
- `shell:input` - Send input to shell
  - **Data**: `{sessionId: "uuid", input: "command\n"}`
- `shell:kill` - Terminate shell session
  - **Data**: `"session-uuid"`

#### Server to Client
- `shell:spawned` - Shell session created
- `shell:output` - Shell output (stdout/stderr)
- `shell:exit` - Shell session ended
- `shell:killed` - Shell session terminated
- `shell:error` - Shell operation error

## Usage Examples

### JavaScript Client Example with Authentication

```javascript
// Connect to Socket.IO with authentication
const socket = io('http://localhost:8080', {
  query: {
    token: 'your-secure-token'
  }
});

// Handle connection events
socket.on('connect', () => {
  console.log('Connected successfully');
  
  // Watch for file changes
  socket.emit('fs:watch', '/home/user/documents');
});

socket.on('connect_error', (error) => {
  console.error('Authentication failed:', error.message);
});

socket.on('fs:change', (data) => {
  console.log('File changed:', data);
});

// Start port monitoring
socket.emit('net:monitor:start', 'both', '127.0.0.1', 2);

socket.on('net:monitor:started', (data) => {
  console.log('Port monitoring started:', data);
});

socket.on('net:port:changes', (data) => {
  data.changes.forEach(change => {
    console.log(`Port ${change.port} ${change.status} on ${change.interface}`);
  });
});

// Stop port monitoring
socket.emit('net:monitor:stop', 'both', '127.0.0.1');

// Spawn interactive shell
socket.emit('shell:spawn', '/bin/bash');
socket.on('shell:spawned', (data) => {
  console.log('Shell spawned:', data.session_id);
  
  // Send command to shell
  socket.emit('shell:input', data.session_id, 'ls -la\n');
});

socket.on('shell:output', (data) => {
  console.log('Shell output:', data.data);
});
```

### cURL Examples with Authentication

```bash
# Set access token for convenience
ACCESS_TOKEN="your-secure-token"

# List directory
curl -H "Authorization: Bearer $ACCESS_TOKEN" \
     "http://localhost:8080/api/fs/listdir?path=/home"

# Create file
curl -X POST http://localhost:8080/api/fs/create \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"path":"/tmp/test.txt","content":"Hello World"}'

# Download file
curl -X POST http://localhost:8080/api/net/download \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://httpbin.org/json","path":"/tmp/downloaded.json"}'

# Get current listening ports
curl -H "Authorization: Bearer $ACCESS_TOKEN" \
     "http://localhost:8080/api/net/ports?protocol=both&interface=any"

# Execute command
curl -X POST http://localhost:8080/api/shell/exec \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"command":"ls -la","workdir":"/tmp"}'

# Health check (no authentication required)
curl http://localhost:8080/health
```

## Port Monitoring Details

The network module uses a passive monitoring approach that reads from `/proc/net/tcp` and `/proc/net/udp` files to detect port changes without generating network traffic. This method:

- **Efficient**: No active port scanning, just file system reads
- **Real-time**: Configurable polling intervals (minimum 1 second)
- **Selective**: Monitor specific protocols (TCP, UDP, or both)
- **Interface filtering**: Monitor specific network interfaces or all
- **Change detection**: Only reports when ports open or close

### Monitoring Parameters

- **Protocol**: `tcp`, `udp`, or `both`
- **Interface**: IP address (`127.0.0.1`, `0.0.0.0`) or `any` for all interfaces
- **Interval**: Polling interval in seconds (minimum 1, recommended 2-5)

### Port Change Events

Each port change includes:
- **Port number**: The affected port
- **Status**: `opened` or `closed`
- **Protocol**: `tcp` or `udp`
- **Interface**: The network interface IP
- **Timestamp**: Unix timestamp of the change

## Deployment Examples

### Docker Compose Example

```yaml
version: '3.8'
services:
  ccw:
    build: .
    ports:
      - "8080:8080"
    environment:
      - AUTH_TOKEN=your-secure-token
      - PORT=8080
    # For debug mode, add:
    # command: ["--debug"]
    restart: unless-stopped
```

### Kubernetes Deployment Example

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ccw-deployment
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ccw
  template:
    metadata:
      labels:
        app: ccw
    spec:
      containers:
      - name: ccw
        image: ccw:latest
        ports:
        - containerPort: 8080
        env:
        - name: AUTH_TOKEN
          valueFrom:
            secretKeyRef:
              name: ccw-secret
              key: auth_token
        - name: PORT
          value: "8080"
        # For debug mode:
        # args: ["--debug"]
---
apiVersion: v1
kind: Secret
metadata:
  name: ccw-secret
type: Opaque
stringData:
  auth_token: "your-secure-token"
```

## Security Considerations

- **Authentication Required**: All API endpoints (except health check) require Bearer token authentication
- **Environment Variables**: Store auth token and sensitive configuration in environment variables
- **Token Security**: Use strong, unique access token for the Bearer token
- **HTTPS Recommended**: Use HTTPS in production to protect authentication tokens in transit
- **Container Security**: The application runs as a non-root user in the Docker container
- **File System Isolation**: File operations are restricted to the container's file system
- **Shell Security**: Shell commands are executed with the application's user permissions
- **Network Monitoring**: Port monitoring only reads system information, doesn't perform network operations
- **Connection Cleanup**: Socket.IO connections are properly cleaned up on disconnect
- **Debug Mode**: Disable debug mode in production to prevent information leakage

## Development

### Project Structure
```
.
├── main.go              # Main application entry point with auth middleware
├── modules/
│   ├── filesystem.go    # File system module implementation  
│   ├── network.go       # Network module implementation
│   └── shell.go         # Shell module implementation
├── go.mod              # Go module dependencies
├── Dockerfile          # Docker container configuration
└── README.md           # This documentation
```

### Building

```bash
# Build locally
go build

# Build Docker image
docker build -t ccw .

# Run with authentication
export AUTH_TOKEN="your-secure-token"
./ccw --debug
```

### Testing Authentication

```bash
# Test without authentication (should fail)
curl http://localhost:8080/api/fs/listdir?path=/

# Test with wrong token (should fail)
curl -H "Authorization: Bearer wrong-token" \
     http://localhost:8080/api/fs/listdir?path=/

# Test with correct token (should succeed)
curl -H "Authorization: Bearer your-secure-token" \
     http://localhost:8080/api/fs/listdir?path=/

# Health check (should work without auth)
curl http://localhost:8080/health
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests if applicable
5. Ensure authentication works correctly
6. Test both debug and production modes
7. Submit a pull request

## License

MIT License