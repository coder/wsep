# Protocol

Each Message is represented as a single WebSocket message. A newline character separates a JSON header from the binary body.

Some messages may omit the body.

The overhead of the additional frame is 2 to 6 bytes. In high throughput cases, messages contain ~32KB of data,
so this overhead is negligible.

### Client Messages

#### Start

This must be the first Client message.

```json
{
  "type": "start",
  "command": {
    "command": "cat",
    "args": ["/dev/urandom"],
    "tty": false
  }
}
```

#### Stdin

```json
{ "type": "stdin" }
```

and a body follows after a newline character.

#### Resize

```json
{ "type": "resize", "cols": 80, "rows": 80 }
```

Only valid on tty messages.

#### CloseStdin

No more Stdin messages may be sent after this.

```json
{ "type": "close_stdin" }
```

### Server Messages

#### Pid

This is sent immediately after the command starts.

```json
{ "type": "pid", "pid": 0 }
```

#### Stdout

```json
{ "type": "stdout" }
```

and a body follows after a newline character.

#### Stderr

```json
{ "type": "stderr" }
```

and a body follows after a newline character.

#### ExitCode

This is the last message sent by the server.

```json
{ "type": "exit_code", "code": 255 }
```

A normal closure follows.
