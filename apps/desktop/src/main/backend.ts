import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import { existsSync } from 'node:fs';
import { createInterface } from 'node:readline';

export type BackendStatus = 'starting' | 'ready' | 'disconnected' | 'error';

export interface BackendConnection {
  readonly baseUrl: string;
  readonly token: string;
  readonly version: string;
}

export interface BackendState {
  readonly status: BackendStatus;
  readonly connection?: BackendConnection;
  readonly message?: string;
}

interface ReadyMessage {
  readonly type: string;
  readonly port: number;
  readonly token: string;
  readonly version: string;
}

export class BackendSupervisor {
  private child: ChildProcessWithoutNullStreams | null = null;
  private current: BackendState = { status: 'starting' };
  private intentionalStop = false;

  public constructor(
    private readonly binaryPath: string,
    private readonly onState: (state: BackendState) => void,
  ) {}

  public get state(): BackendState {
    return this.current;
  }

  public start(): void {
    if (this.child) return;
    this.intentionalStop = false;
    this.setState({ status: 'starting' });
    if (!existsSync(this.binaryPath)) {
      this.setState({
        status: 'error',
        message: `Backend executable not found: ${this.binaryPath}`,
      });
      return;
    }

    const child = spawn(this.binaryPath, [], {
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
    });
    this.child = child;
    const lines = createInterface({ input: child.stdout });
    let receivedReady = false;
    const timeout = setTimeout(() => {
      if (!receivedReady) {
        this.setState({ status: 'error', message: 'The backend did not become ready in time.' });
        child.kill();
      }
    }, 10_000);

    lines.once('line', (line) => {
      try {
        const ready = JSON.parse(line) as ReadyMessage;
        if (ready.type !== 'ready' || !ready.port || !ready.token)
          throw new Error('Invalid handshake');
        receivedReady = true;
        clearTimeout(timeout);
        this.setState({
          status: 'ready',
          connection: {
            baseUrl: `http://127.0.0.1:${ready.port.toString()}`,
            token: ready.token,
            version: ready.version,
          },
        });
      } catch (error) {
        this.setState({
          status: 'error',
          message: error instanceof Error ? error.message : 'Invalid backend handshake.',
        });
        child.kill();
      }
    });

    child.stderr.on('data', (data: Buffer) =>
      console.error(`[backend] ${data.toString().trimEnd()}`),
    );
    child.once('error', (error) => this.setState({ status: 'error', message: error.message }));
    child.once('exit', (code) => {
      clearTimeout(timeout);
      lines.close();
      if (this.child === child) this.child = null;
      if (!this.intentionalStop) {
        this.setState({
          status: 'disconnected',
          message: `Backend exited with code ${String(code)}.`,
        });
      }
    });
  }

  public restart(): void {
    this.stop(() => this.start());
  }

  public stop(afterStop?: () => void): void {
    const child = this.child;
    if (!child) {
      afterStop?.();
      return;
    }
    this.intentionalStop = true;
    child.stdin.end();
    const forceTimer = setTimeout(() => child.kill(), 3_500);
    child.once('exit', () => {
      clearTimeout(forceTimer);
      if (this.child === child) this.child = null;
      afterStop?.();
    });
  }

  private setState(state: BackendState): void {
    this.current = state;
    this.onState(state);
  }
}
