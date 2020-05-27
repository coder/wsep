// "wsep" browser client implementation

// ExitCode contains process exit information
export interface ExitCode {
  exit_code: number;
}

const DELIMITER = '\n';

// Command describes initialization parameters for a remote command
export interface Command {
  command: string;
  args: string[];
  tty: boolean;
  uid: number;
  gid: number;
  env: string[];
  working_dir: string;
}
type ClientHeader = { type: 'start'; command: Command } | { type: 'stdin' };

type ServerHeader =
  | { type: 'stdout' }
  | { type: 'stderr' }
  | { type: 'pid'; pid: number }
  | { type: 'exit_code'; exit_code: number };

type Header = ClientHeader | ServerHeader;

export class Process {
  // process id of the remote process
  public pid?: number = undefined;

  // stdout stream of the remote process
  public stdout: ReadableStream;

  // stderr stream of the remote process
  public stderr: ReadableStream;

  // exit_code resolves when the process exits (0 or nonzero)
  // the Promise rejects if the command execution is interrupted by a network or otherwise unexpected error
  public exit_code: Promise<ExitCode>;

  private conn: WebSocket;
  private resolve: (code: ExitCode) => void;
  private reject: (reason: any) => void;

  constructor(conn: WebSocket, command: Command) {
    this.conn = conn;
    this.conn.onmessage = this.handleMessage;
    this.conn.onerror = this.handleError;
    this.conn.onclose = this.handleClose;

    this.start(command);
    this.exit_code = new Promise((res, rej) => {
      this.resolve = res;
      this.reject = rej;
    });
  }

  private start = async (command: Command): Promise<void> => {
    const msg = await joinMessage({ type: 'start', command: command });
    this.conn.send(msg);
  };

  private handleMessage = async (ev: MessageEvent) => {
    const [header, body] = await splitMessage(ev.data);
    switch (header.type) {
      case 'exit_code':
        const { exit_code } = header;
        this.resolve({ exit_code: exit_code });
        break;
      case 'pid':
        this.pid = header.pid;
        break;
      case 'stderr':
        break;
      case 'stdout':
        break;
      default:
    }
  };

  private handleClose = (ev: CloseEvent) => {
    this.reject(ev);
  };

  private handleError = (ev: Event) => {
    this.reject(ev);
  };

  public resize = async (rows: number, cols: number): Promise<void> => {};
}

const joinMessage = async (header: Header, body?: Blob): Promise<Blob> => {
  const encodedHeader = JSON.stringify(header);
  if (body) {
    return new Blob([encodedHeader, DELIMITER, encodedHeader], { type: '' });
  }
  return new Blob([encodedHeader, DELIMITER], { type: '' });
};

const splitMessage = async (message: Blob): Promise<[Header, Blob]> => {
  return [{ type: 'exit_code', exit_code: 0 }, new Blob()];
};
