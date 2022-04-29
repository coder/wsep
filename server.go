package wsep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/armon/circbuf"
	"github.com/google/uuid"

	"go.coder.com/flog"
	"golang.org/x/sync/errgroup"
	"golang.org/x/xerrors"
	"nhooyr.io/websocket"

	"cdr.dev/wsep/internal/proto"
)

var reconnectingProcesses sync.Map

// Options allows configuring the server.
type Options struct {
	ReconnectingProcessTimeout time.Duration
}

// Serve runs the server-side of wsep.
// The execer may be another wsep connection for chaining.
// Use LocalExecer for local command execution.
func Serve(ctx context.Context, c *websocket.Conn, execer Execer, options *Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if options == nil {
		options = &Options{}
	}
	if options.ReconnectingProcessTimeout == 0 {
		options.ReconnectingProcessTimeout = 5 * time.Minute
	}

	c.SetReadLimit(maxMessageSize)
	var (
		header    proto.Header
		process   Process
		wsNetConn = websocket.NetConn(ctx, c, websocket.MessageBinary)
	)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, byt, err := c.Read(ctx)
		if xerrors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			status := websocket.CloseStatus(err)
			if status == -1 {
				return xerrors.Errorf("read message: %w", err)
			}
			if status != websocket.StatusNormalClosure {
				return err
			}
			return nil
		}
		headerByt, bodyByt := proto.SplitMessage(byt)

		err = json.Unmarshal(headerByt, &header)
		if err != nil {
			return xerrors.Errorf("unmarshal header: %w", err)
		}

		switch header.Type {
		case proto.TypeStart:
			if process != nil {
				return errors.New("command already started")
			}

			var header proto.ClientStartHeader
			err = json.Unmarshal(byt, &header)
			if err != nil {
				return xerrors.Errorf("unmarshal start header: %w", err)
			}

			command := mapToClientCmd(header.Command)

			// Only allow TTYs with IDs to be reconnected.
			if command.TTY && header.ID != "" {
				// Enforce a consistent format for IDs.
				_, err := uuid.Parse(header.ID)
				if err != nil {
					flog.Error("%s is not a valid uuid: %w", header.ID, err)
				}

				// Get an existing process or create a new one.
				var rprocess *reconnectingProcess
				rawRProcess, ok := reconnectingProcesses.Load(header.ID)
				if ok {
					rprocess, ok = rawRProcess.(*reconnectingProcess)
					if !ok {
						flog.Error("found invalid type in reconnecting process map for ID %s", header.ID)
					}
					process = rprocess.process
				} else {
					// The process will be kept alive as long as this context does not
					// finish (and as long as the process does not exit on its own).  This
					// is a new context since the parent context finishes when the request
					// ends which would kill the process prematurely.
					ctx, cancel := context.WithCancel(context.Background())

					// The process will be killed if the provided context ends.
					process, err = execer.Start(ctx, command)
					if err != nil {
						cancel()
						return err
					}

					// Default to buffer 64KB.
					ringBuffer, err := circbuf.NewBuffer(64 * 1024)
					if err != nil {
						cancel()
						return xerrors.Errorf("unable to create ring buffer %w", err)
					}

					rprocess = &reconnectingProcess{
						activeConns: make(map[string]net.Conn),
						process:     process,
						// Timeouts created with AfterFunc can be reset.
						timeout:    time.AfterFunc(options.ReconnectingProcessTimeout, cancel),
						ringBuffer: ringBuffer,
					}
					reconnectingProcesses.Store(header.ID, rprocess)

					// If the process exits send the exit code to all listening
					// connections then close everything.
					go func() {
						err = process.Wait()
						code := 0
						if exitErr, ok := err.(ExitError); ok {
							code = exitErr.Code
						}
						rprocess.activeConnsMutex.Lock()
						for _, conn := range rprocess.activeConns {
							_ = sendExitCode(ctx, code, conn)
						}
						rprocess.activeConnsMutex.Unlock()
						rprocess.Close()
						reconnectingProcesses.Delete(header.ID)
					}()

					// Write to the ring buffer and all connections as we receive stdout.
					go func() {
						buffer := make([]byte, 32*1024)
						for {
							read, err := rprocess.process.Stdout().Read(buffer)
							if err != nil {
								// When the process is closed this is triggered.
								break
							}
							part := buffer[:read]
							_, err = rprocess.ringBuffer.Write(part)
							if err != nil {
								flog.Error("reconnecting process %s write buffer: %v", header.ID, err)
								cancel()
								break
							}
							rprocess.activeConnsMutex.Lock()
							for _, conn := range rprocess.activeConns {
								_ = sendOutput(ctx, part, conn)
							}
							rprocess.activeConnsMutex.Unlock()
						}
					}()
				}

				err = sendPID(ctx, process.Pid(), wsNetConn)
				if err != nil {
					flog.Error("failed to send pid %d", process.Pid())
				}

				// Write out the initial contents in the ring buffer.
				err = sendOutput(ctx, rprocess.ringBuffer.Bytes(), wsNetConn)
				if err != nil {
					return xerrors.Errorf("write reconnecting process %s buffer: %w", header.ID, err)
				}

				// Store this connection on the reconnecting process.  All connections
				// stored on the process will receive the process's stdout.
				connectionID := uuid.NewString()
				rprocess.activeConnsMutex.Lock()
				rprocess.activeConns[connectionID] = wsNetConn
				rprocess.activeConnsMutex.Unlock()

				// Keep resetting the inactivity timer while this connection is alive.
				rprocess.timeout.Reset(options.ReconnectingProcessTimeout)
				heartbeat := time.NewTicker(options.ReconnectingProcessTimeout / 2)
				defer heartbeat.Stop()
				go func() {
					for {
						select {
						// Stop looping once this request finishes.
						case <-ctx.Done():
							return
						case <-heartbeat.C:
						}
						rprocess.timeout.Reset(options.ReconnectingProcessTimeout)
					}
				}()

				// Remove this connection from the process's connection list once the
				// connection ends so data is no longer sent to it.
				defer func() {
					wsNetConn.Close() // REVIEW@asher: Not sure if necessary.
					rprocess.activeConnsMutex.Lock()
					delete(rprocess.activeConns, connectionID)
					rprocess.activeConnsMutex.Unlock()
				}()
			} else {
				process, err = execer.Start(ctx, command)
				if err != nil {
					return err
				}

				err = sendPID(ctx, process.Pid(), wsNetConn)
				if err != nil {
					flog.Error("failed to send pid %d", process.Pid())
				}

				var outputgroup errgroup.Group
				outputgroup.Go(func() error {
					return copyWithHeader(process.Stdout(), wsNetConn, proto.Header{Type: proto.TypeStdout})
				})
				outputgroup.Go(func() error {
					return copyWithHeader(process.Stderr(), wsNetConn, proto.Header{Type: proto.TypeStderr})
				})

				go func() {
					defer wsNetConn.Close()
					_ = outputgroup.Wait()
					err = process.Wait()
					if exitErr, ok := err.(ExitError); ok {
						_ = sendExitCode(ctx, exitErr.Code, wsNetConn)
						return
					}
					_ = sendExitCode(ctx, 0, wsNetConn)
				}()

				defer func() {
					process.Close()
				}()
			}
		case proto.TypeResize:
			if process == nil {
				return errors.New("resize sent before command started")
			}

			var header proto.ClientResizeHeader
			err = json.Unmarshal(byt, &header)
			if err != nil {
				return xerrors.Errorf("unmarshal resize header: %w", err)
			}

			err = process.Resize(ctx, header.Rows, header.Cols)
			if err != nil {
				return xerrors.Errorf("resize: %w", err)
			}
		case proto.TypeStdin:
			_, err := io.Copy(process.Stdin(), bytes.NewReader(bodyByt))
			if err != nil {
				return xerrors.Errorf("read stdin: %w", err)
			}
		case proto.TypeCloseStdin:
			err = process.Stdin().Close()
			if err != nil {
				return xerrors.Errorf("close stdin: %w", err)
			}
		default:
			flog.Error("unrecognized header type: %s", header.Type)
		}
	}
}

func sendExitCode(_ context.Context, exitCode int, conn net.Conn) error {
	header, err := json.Marshal(proto.ServerExitCodeHeader{
		Type:     proto.TypeExitCode,
		ExitCode: exitCode,
	})
	if err != nil {
		return err
	}
	_, err = proto.WithHeader(conn, header).Write(nil)
	return err
}

func sendPID(_ context.Context, pid int, conn net.Conn) error {
	header, err := json.Marshal(proto.ServerPidHeader{Type: proto.TypePid, Pid: pid})
	if err != nil {
		return err
	}
	_, err = proto.WithHeader(conn, header).Write(nil)
	return err
}

func sendOutput(_ context.Context, data []byte, conn net.Conn) error {
	header, err := json.Marshal(proto.ServerPidHeader{Type: proto.TypeStdout})
	if err != nil {
		return err
	}
	_, err = proto.WithHeader(conn, header).Write(data)
	return err
}

func copyWithHeader(r io.Reader, w io.Writer, header proto.Header) error {
	headerByt, err := json.Marshal(header)
	if err != nil {
		return err
	}
	wr := proto.WithHeader(w, headerByt)
	_, err = io.Copy(wr, r)
	if err != nil {
		return err
	}
	return nil
}

type reconnectingProcess struct {
	activeConnsMutex sync.Mutex
	activeConns      map[string]net.Conn

	ringBuffer *circbuf.Buffer
	timeout    *time.Timer
	process    Process
}

// Close ends all connections to the reconnecting process and clears the ring
// buffer.
func (r *reconnectingProcess) Close() {
	r.activeConnsMutex.Lock()
	defer r.activeConnsMutex.Unlock()
	for _, conn := range r.activeConns {
		_ = conn.Close()
	}
	_ = r.process.Close()
	r.ringBuffer.Reset()
}
