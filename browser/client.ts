// "wsep" browser client reference implementation

const DELIMITER = '\n'.charCodeAt(0);

// Command describes initialization parameters for a remote command
export interface Command {
  command: string;
  args?: string[];
  tty?: boolean;
  uid?: number;
  gid?: number;
  env?: string[];
  working_dir?: string;
}

export type ClientHeader =
  | { type: 'start'; id: string; command: Command; cols: number; rows: number; }
  | { type: 'stdin' }
  | { type: 'close_stdin' }
  | { type: 'resize'; cols: number; rows: number };

export type ServerHeader =
  | { type: 'stdout' }
  | { type: 'stderr' }
  | { type: 'pid'; pid: number }
  | { type: 'exit_code'; exit_code: number };

export type Header = ClientHeader | ServerHeader;

export const setBinaryType = (ws: WebSocket) => {
  ws.binaryType = 'arraybuffer';
};

export const sendStdin = (ws: WebSocket, data: Uint8Array) => {
  if (data.byteLength < 1) return;
  const msg = joinMessage({ type: 'stdin' }, data);
  ws.send(msg.buffer);
};

export const closeStdin = (ws: WebSocket) => {
  const msg = joinMessage({ type: 'close_stdin' });
  ws.send(msg.buffer);
};

export const startCommand = (
  ws: WebSocket,
  command: Command,
  id: string,
  rows: number,
  cols: number
) => {
  const msg = joinMessage({ type: 'start', command, id, rows, cols });
  ws.send(msg.buffer);
};

export const parseServerMessage = (
  ev: MessageEvent
): [ServerHeader, Uint8Array] => {
  const [header, body] = splitMessage(ev.data);
  return [header as ServerHeader, body];
};

export const resizeTerminal = (
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
