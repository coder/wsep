// "wsep" browser client reference implementation

const DELIMITER = '\n'.charCodeAt(0);

// Command describes initialization parameters for a remote command
interface Command {
  command: string;
  args?: string[];
  tty?: boolean;
  rows?: number;
  cols?: number;
  uid?: number;
  gid?: number;
  env?: string[];
  working_dir?: string;
}

type ClientHeader =
  | { type: 'start'; id: string; command: Command; }
  | { type: 'stdin' }
  | { type: 'close_stdin' }
  | { type: 'resize'; cols: number; rows: number };

type ServerHeader =
  | { type: 'stdout' }
  | { type: 'stderr' }
  | { type: 'pid'; pid: number }
  | { type: 'exit_code'; exit_code: number };

type Header = ClientHeader | ServerHeader;

const setBinaryType = (ws: WebSocket) => {
  ws.binaryType = 'arraybuffer';
};

const sendStdin = (ws: WebSocket, data: Uint8Array) => {
  if (data.byteLength < 1) return;
  const msg = joinMessage({ type: 'stdin' }, data);
  ws.send(msg.buffer);
};

const closeStdin = (ws: WebSocket) => {
  const msg = joinMessage({ type: 'close_stdin' });
  ws.send(msg.buffer);
};

const startCommand = (
  ws: WebSocket,
  command: Command,
  id: string,
) => {
  const msg = joinMessage({ type: 'start', command, id });
  ws.send(msg.buffer);
};

const parseServerMessage = (
  ev: MessageEvent
): [ServerHeader, Uint8Array] => {
  const [header, body] = splitMessage(ev.data);
  return [header as ServerHeader, body];
};

const resizeTerminal = (
  ws: WebSocket,
  rows: number,
  cols: number
): void => {
  const msg = joinMessage({ type: 'resize', cols, rows });
  ws.send(msg.buffer);
};

const joinMessage = (header: ClientHeader, body?: Uint8Array): Uint8Array => {
  const encodedHeader = new TextEncoder().encode(JSON.stringify(header));
  if (body && body.length > 0) {
    const tmp = new Uint8Array(encodedHeader.byteLength + 1 + body.byteLength);
    tmp.set(encodedHeader, 0);
    tmp.set([DELIMITER], encodedHeader.byteLength);
    tmp.set(body, encodedHeader.byteLength + 1);
    return tmp;
  }
  return encodedHeader;
};

const splitMessage = (message: ArrayBuffer): [Header, Uint8Array] => {
  let array: Uint8Array;
  if (typeof message === 'string') {
    array = new TextEncoder().encode(message);
  } else {
    array = new Uint8Array(message);
  }

  for (let i = 0; i < array.length; i++) {
    if (array[i] === DELIMITER) {
      const headerText = new TextDecoder().decode(array.slice(0, i));
      const header: ServerHeader = JSON.parse(headerText);
      const body =
        array.length > i + 1
          ? array.slice(i + 1, array.length)
          : new Uint8Array(0);
      return [header, body];
    }
  }

  return [JSON.parse(new TextDecoder().decode(array)), new Uint8Array(0)];
};

const log = (output: HTMLElement | null, message: string) => {
  if (output) {
    output.innerText = message + "\n"
  }
  console.log(message)
}

const main = () => {
  const ws = new WebSocket(location.origin.replace(/^http/, "ws"))
  setBinaryType(ws)

  const output = document.getElementById("output")

  // @ts-ignore Not sure how to make this work.
  const term = new Terminal();
  // @ts-ignore Not sure how to make this work.
  const fit = new FitAddon.FitAddon();
  term.loadAddon(fit);
  term.open(document.getElementById("terminal"));
  fit.fit();

  ws.addEventListener("open", () => {
    log(output, "sending start command...")
    startCommand(ws, {
      command: "bash",
      tty: true,
      rows: term.rows,
      cols: term.cols,
      env: ["TERM=xterm"],
    }, "id")
  })

  ws.addEventListener("message", (ev) => {
    const [header, body] = splitMessage(ev.data)

    switch (header.type) {
      case "stdout":
      case "stderr":
        term.write(body)
        if (output && body) {
          output.innerText = new TextDecoder().decode(body)
        }
        break
      case "pid":
        if (output) {
          log(output, "ready")
        }
        break
      case "exit_code":
        log(output, "exited")
        break
      default:
        log(output, "unknown message type " + header.type)
        break
    }
  })

  const form = document.getElementById("input") as HTMLFormElement
  if (form) {
    form.addEventListener("submit", (ev) => {
      ev.preventDefault()
      const formData = new FormData(form);
      const input = formData.get("input") as string + "\n"
      if (input) {
        const bytes = new TextEncoder().encode(input)
        sendStdin(ws, bytes)
        form.reset()
      }
    })
  }

  term.onData((data: string) => {
    const bytes = new TextEncoder().encode(data)
    sendStdin(ws, bytes)
  })
}

main()
