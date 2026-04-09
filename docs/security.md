# Security

MediaMolder validates all inputs at the system boundary to prevent common
attacks in media processing pipelines.

## URL Scheme Allowlist

Only approved URL schemes are accepted for input and output URLs:

| Scheme | Purpose |
|--------|---------|
| `file` | Local file access |
| `http` | HTTP streaming |
| `https` | Secure HTTP streaming |
| `rtmp` | RTMP live streaming |
| `rtsp` | RTSP camera feeds |
| `srt` | SRT low-latency streaming |

Unknown schemes (e.g., `ftp`, `gopher`, `javascript`) are rejected with a
clear error message.

### Custom Allowlists

```go
sc := pipeline.SecurityConfig{
    AllowedSchemes: []string{"file", "https", "srt"},
}
```

## Path Traversal Protection

File paths are validated to prevent directory traversal attacks:

1. **Clean**: `filepath.Clean()` normalizes the path
2. **Reject `..`**: Any remaining `..` components are rejected
3. **Resolve symlinks**: `filepath.EvalSymlinks()` resolves symbolic links
4. **Base directory check**: The resolved path must be under `BaseDir`

### Configuration

```go
sc := pipeline.SecurityConfig{
    BaseDir: "/var/media",
}
err := sc.ValidateURL("/var/media/../etc/passwd")
// Returns: "path traversal detected"
```

## Resource Limits

### Decode Dimensions

```go
sc := pipeline.SecurityConfig{
    MaxWidth:  7680,  // 8K max
    MaxHeight: 4320,
}
```

Frames exceeding these dimensions are rejected before decoding.

### Stream Count

```go
sc := pipeline.SecurityConfig{
    MaxStreams: 64,
}
```

Inputs with more streams than this limit are rejected.

### Probe Timeout

```go
sc := pipeline.SecurityConfig{
    ProbeTimeout: 10, // seconds
}
```

Format probing is bounded to prevent slowloris-style attacks from
malicious media files.

## Pipeline Resource Limits

### Concurrent Pipelines

```go
limiter := pipeline.NewConcurrencyLimiter(16)

if !limiter.TryAcquire() {
    return errors.New("too many concurrent pipelines")
}
defer limiter.Release()
```

### Memory and CPU

```go
sc := pipeline.SecurityConfig{
    MemoryCapMB: 4096,  // 4 GB
    MaxThreads:  8,
}
```

## Multi-Tenant Recommendations

For deployments serving multiple tenants:

1. **Isolate file access**: Set `BaseDir` to a per-tenant directory
2. **Restrict schemes**: Remove `file` from the allowlist for untrusted inputs
3. **Set strict limits**: Lower `MaxWidth`/`MaxHeight`, `MaxStreams`, and `MaxConcurrentPipelines`
4. **Monitor**: Enable Prometheus metrics and set alerts on error rates
5. **Separate crash reports**: Configure per-tenant crash report directories

## Full Validation Example

```go
sc := pipeline.DefaultSecurityConfig()
sc.BaseDir = "/var/media/tenant-123"

if err := sc.ValidateConfig(cfg); err != nil {
    log.Fatalf("config validation failed: %v", err)
}

pipeline, err := pipeline.NewPipeline(cfg)
```
