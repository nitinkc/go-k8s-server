package main

import (
    "bufio"
    "fmt"
    "net"
    "net/http"
    "os"
    "strings"
    "time"
)

func main() {
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    // Root endpoint
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "Hello from Kubernetes! Go app is running successfully.")
    })

    // Health check endpoint for K8s probes
    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "OK")
    })

    // Redis connectivity check — PINGs the redis-db-service ClusterIP
    http.HandleFunc("/redis-check", func(w http.ResponseWriter, r *http.Request) {
        redisAddr := os.Getenv("REDIS_ADDR")
        if redisAddr == "" {
            redisAddr = "redis-db-service:6379" // From clusterIP Service
        }

        conn, err := net.DialTimeout("tcp", redisAddr, 2*time.Second)
        if err != nil {
            http.Error(w, fmt.Sprintf("Redis is DOWN at %s: %v", redisAddr, err), http.StatusServiceUnavailable)
            return
        }
        defer conn.Close()

        conn.SetDeadline(time.Now().Add(2 * time.Second))
        if _, err := conn.Write([]byte("PING\r\n")); err != nil {
            http.Error(w, fmt.Sprintf("Redis is DOWN at %s: %v", redisAddr, err), http.StatusServiceUnavailable)
            return
        }

        reply, err := bufio.NewReader(conn).ReadString('\n')
        if err != nil || !strings.HasPrefix(reply, "+PONG") {
            http.Error(w, fmt.Sprintf("Redis is DOWN at %s: unexpected reply %q (err=%v)", redisAddr, reply, err), http.StatusServiceUnavailable)
            return
        }

        fmt.Fprintf(w, "Redis is UP at %s: %s", redisAddr, strings.TrimSpace(reply))
    })

    fmt.Printf("Server starting on port %s...\n", port)
    http.ListenAndServe(":"+port, nil)
}