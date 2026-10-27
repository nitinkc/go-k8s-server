package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "io"
    "net"
    "net/http"
    "os"
    "strconv"
    "strings"
    "time"
)

var redisAddr = getEnv("REDIS_ADDR", "redis-db-service:6379")

func getEnv(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}

// redisDo sends one RESP command and returns the parsed reply.
func redisDo(args ...string) (interface{}, error) {
    conn, err := net.DialTimeout("tcp", redisAddr, 2*time.Second)
    if err != nil {
        return nil, err
    }
    defer conn.Close()
    conn.SetDeadline(time.Now().Add(2 * time.Second))

    var b strings.Builder
    fmt.Fprintf(&b, "*%d\r\n", len(args))
    for _, a := range args {
        fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
    }
    if _, err := conn.Write([]byte(b.String())); err != nil {
        return nil, err
    }
    return readReply(bufio.NewReader(conn))
}

func readReply(r *bufio.Reader) (interface{}, error) {
    line, err := r.ReadString('\n')
    if err != nil {
        return nil, err
    }
    line = strings.TrimRight(line, "\r\n")
    if line == "" {
        return nil, fmt.Errorf("empty reply")
    }
    switch line[0] {
    case '+':
        return line[1:], nil
    case '-':
        return nil, fmt.Errorf("redis error: %s", line[1:])
    case ':':
        return strconv.ParseInt(line[1:], 10, 64)
    case '$':
        n, _ := strconv.Atoi(line[1:])
        if n < 0 {
            return nil, nil
        }
        buf := make([]byte, n+2)
        if _, err := io.ReadFull(r, buf); err != nil {
            return nil, err
        }
        return string(buf[:n]), nil
    case '*':
        n, _ := strconv.Atoi(line[1:])
        if n < 0 {
            return nil, nil
        }
        items := make([]interface{}, n)
        for i := range items {
            if items[i], err = readReply(r); err != nil {
                return nil, err
            }
        }
        return items, nil
    }
    return nil, fmt.Errorf("unexpected reply: %q", line)
}

type LoginRecord struct {
    Timestamp  string `json:"timestamp"`
    PodName    string `json:"pod_name"`
    PodIP      string `json:"pod_ip"`
    NodeName   string `json:"node_name"`
    ClientAddr string `json:"client_addr"`
}

func main() {
    port := getEnv("PORT", "8080")

    // Root: simple page with a "login" button + saved records
    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html")
        fmt.Fprint(w, `<!doctype html>
<html><body>
<h2>Go k8s server — Redis persistence POC</h2>
<button onclick="save()">Simulate Login &amp; Save to Redis</button>
<pre id="out"></pre>
<script>
async function save() {
  const r = await fetch('/login', {method: 'POST'});
  document.getElementById('out').textContent = await r.text();
  load();
}
async function load() {
  const r = await fetch('/logins');
  const data = await r.json();
  document.getElementById('out').textContent =
    'Saved records (' + data.length + '):\n' + JSON.stringify(data, null, 2);
}
load();
</script>
</body></html>`)
    })

    // Health check endpoint for K8s probes
    http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        fmt.Fprintf(w, "OK")
    })

    // Button-click endpoint: record login timestamp + pod/node details in Redis
    http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost {
            http.Error(w, "POST only", http.StatusMethodNotAllowed)
            return
        }
        hostname, _ := os.Hostname()
        rec := LoginRecord{
            Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
            PodName:    getEnv("POD_NAME", hostname),
            PodIP:      getEnv("POD_IP", ""),
            NodeName:   getEnv("NODE_NAME", ""),
            ClientAddr: r.RemoteAddr,
        }
        data, _ := json.Marshal(rec)
        if _, err := redisDo("RPUSH", "logins", string(data)); err != nil {
            http.Error(w, fmt.Sprintf("failed to save to Redis at %s: %v", redisAddr, err), http.StatusServiceUnavailable)
            return
        }
        fmt.Fprintf(w, "Saved: %s\n", data)
    })

    // List saved login records — survives Redis restarts when Redis has a PVC
    http.HandleFunc("/logins", func(w http.ResponseWriter, r *http.Request) {
        reply, err := redisDo("LRANGE", "logins", "0", "-1")
        if err != nil {
            http.Error(w, fmt.Sprintf("Redis read failed at %s: %v", redisAddr, err), http.StatusServiceUnavailable)
            return
        }
        w.Header().Set("Content-Type", "application/json")
        items, _ := reply.([]interface{})
        w.Write([]byte("["))
        for i, it := range items {
            if i > 0 {
                w.Write([]byte(","))
            }
            if s, ok := it.(string); ok {
                w.Write([]byte(s))
            }
        }
        w.Write([]byte("]"))
    })

    // Redis connectivity check — PINGs the redis-db-service ClusterIP
    http.HandleFunc("/redis-check", func(w http.ResponseWriter, r *http.Request) {
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
