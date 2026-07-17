# Pesquisa: Bibliotecas Go PTY para Windows

> Pesquisa realizada em 2026-07-15

---

## 1. creack/pty — ❌ NÃO suporta Windows

- **URL:** https://github.com/creack/pty
- **Stars:** ~2.063 | **Última atualização:** Jul 2026 (muito ativo)
- **Status Windows:** ❌ **Não funcional.** Retorna `ErrUnsupported`. O arquivo `start_windows.go` é apenas stub para compilação:

```go
//go:build windows
func StartWithSize(cmd *exec.Cmd, ws *Winsize) (*os.File, error) {
    return nil, ErrUnsupported
}
```

- **Veredito:** Use no Unix/macOS. **Não serve para Windows.**

---

## 2. UserExistsError/conpty — ✅ RECOMENDADO

- **URL:** https://github.com/UserExistsError/conpty
- **Stars:** 43 | **Última atualização:** Jun 2026 | **Issues abertas:** 0 | **Licença:** sim
- **Status Windows:** ✅ **Confirmado funcionando.** Wrapper nativo ConPTY via `golang.org/x/sys/windows`. Sem DLLs externas.
- **Requisito:** Windows 10 1809+ (build 17763)

### API completa

```go
// Verificar disponibilidade
conpty.IsConPtyAvailable() bool

// Iniciar processo em PTY
cpty, err := conpty.Start(commandLine string, options ...ConPtyOption) (*ConPty, error)

// Options
conpty.ConPtyDimensions(width, height int)  // tamanho inicial
conpty.ConPtyWorkDir(dir string)            // diretório de trabalho
conpty.ConPtyEnv(env []string)              // variáveis de ambiente

// ConPty implementa io.Reader e io.Writer
cpty.Read(p []byte) (int, error)    // lê saída
cpty.Write(p []byte) (int, error)   // envia texto/comandos

// Lifecycle
cpty.Wait(ctx context.Context) (exitCode uint32, err error)
cpty.Close() error
cpty.Resize(w, h int) error
cpty.Pid() int
```

### Exemplo

```go
package main

import (
    "context"
    "log"
    "time"
    "github.com/UserExistsError/conpty"
)

func main() {
    cpty, err := conpty.Start(`c:\windows\system32\cmd.exe`)
    if err != nil { log.Fatalf("Failed: %v", err) }
    defer cpty.Close()

    cpty.Write([]byte("@echo off\r\n"))
    cpty.Write([]byte("echo hello\r\n"))
    time.Sleep(time.Second)

    out := make([]byte, 1000)
    n, _ := cpty.Read(out)
    log.Printf("Read: %s", string(out[:n]))

    cpty.Write([]byte("exit 1234\r\n"))
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    exitCode, _ := cpty.Wait(ctx)
    log.Printf("ExitCode: %d", exitCode)
}
```

---

## 3. Alternativas

| Biblioteca | URL | Stars | Avaliação |
|---|---|---|---|
| **iamacarpet/go-winpty** | https://github.com/iamacarpet/go-winpty | 64 | ⚠️ Legacy. Requer `winpty.dll` + `winpty-agent.exe`. WinPTY depreciado. |
| **runletapp/go-console** | https://github.com/runletapp/go-console | 49 | 🟡 Cross-platform, mas usa WinPTY legacy no Windows, não ConPTY. |
| **qsocket/conpty-go** | https://github.com/qsocket/conpty-go | 9 | 🔴 Pouco documentado, pouca adoção. |

---

## 4. Spawn de PowerShell / WSL / Git Bash

```go
// PowerShell 5.1
cpty, err := conpty.Start(`powershell.exe -NoProfile`)

// PowerShell 7+
cpty, err := conpty.Start(`pwsh.exe -NoProfile`)

// WSL
cpty, err := conpty.Start(`wsl.exe -d Ubuntu`)

// Git Bash
cpty, err := conpty.Start(`C:\Program Files\Git\bin\bash.exe --login -i`)

// Com options
cpty, err := conpty.Start(
    `pwsh.exe -NoProfile`,
    conpty.ConPtyDimensions(120, 40),
    conpty.ConPtyWorkDir(`C:\Users\Eric\projects`),
    conpty.ConPtyEnv([]string{"TERM=xterm-256color"}),
)
```

**Nota:** `\r\n` (CRLF) é necessário ao enviar comandos — ConPTY espera carriage return.

---

## 5. Leitura do output stream

### Padrão A — Goroutine + io.Copy

```go
go func() {
    io.Copy(os.Stdout, cpty)  // bloqueia até EOF/Close
}()
```

### Padrão B — Loop bloqueante

```go
buf := make([]byte, 4096)
for {
    n, err := cpty.Read(buf)
    if n > 0 {
        chunk := buf[:n]
        // processar chunk
    }
    if err != nil { break }
}
```

### Padrão C — Canal para WebSocket/xterm.js

```go
outputCh := make(chan []byte, 256)

go func() {
    defer close(outputCh)
    buf := make([]byte, 4096)
    for {
        n, err := cpty.Read(buf)
        if n > 0 {
            data := make([]byte, n)
            copy(data, buf[:n])
            outputCh <- data
        }
        if err != nil { return }
    }
}()

for data := range outputCh {
    sendToWebSocket(data)
}
```

---

## Recomendação Final

**Use `UserExistsError/conpty` para Windows.**

1. ConPTY nativo (API moderna, não WinPTY legacy)
2. Sem DLLs externas, só `golang.org/x/sys`
3. API completa: Start, Read, Write, Wait, Close, Resize, Pid
4. Ativamente mantido (0 issues abertas, Jun 2026)
5. Funciona com PowerShell, WSL e Git Bash

**Cross-platform futuro:** interface comum + `creack/pty` (Unix) + `UserExistsError/conpty` (Windows).