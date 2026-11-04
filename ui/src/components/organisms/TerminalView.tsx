import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { Hand, Undo2 } from "lucide-react";
import { Button } from "@/components/atoms/Button";
import { StatusDot, DotTone } from "@/components/atoms/StatusDot";
import { terminalTheme } from "@/lib/terminal-theme";
import { openSocket } from "@/lib/host";

// A live terminal session over a websocket (internal/platform/terminal). Everyone
// watches; Take control sends your keys, and the server drops keys from
// anyone not in control. The session has one size: this view asks for the
// size that fills its space, and shows whatever size the server says.
// Mount one per session: the server closes the socket when it ends.

type Seat = "connecting" | "watching" | "in control" | "someone else" | "ended";

const SEATS: Record<Seat, { tone: DotTone; label: string }> = {
  connecting: { tone: "muted", label: "Connecting" },
  watching: { tone: "success", label: "Watching" },
  "in control": { tone: "warning", label: "You have control: typing goes to the session" },
  "someone else": { tone: "info", label: "Someone else has control" },
  ended: { tone: "muted", label: "Session ended" },
};

interface TerminalViewProps {
  url: string;
  title: string;
  takeControlOnConnect?: boolean; // for a session that is yours to type in
}

interface State {
  you: string;
  controller: string;
  cols: number;
  rows: number;
}

export function TerminalView({ url, title, takeControlOnConnect = false }: TerminalViewProps) {
  const area = useRef<HTMLDivElement>(null);
  const screen = useRef<HTMLDivElement>(null);
  const socket = useRef<WebSocket | null>(null);
  const [seat, setSeat] = useState<Seat>("connecting");

  useEffect(() => {
    const term = new Terminal({ fontSize: 13, cursorBlink: true, theme: terminalTheme() });
    // Follow the page's theme when it changes.
    const themeWatch = new MutationObserver(() => (term.options.theme = terminalTheme()));
    themeWatch.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(screen.current!);

    const ws = openSocket(url);
    ws.binaryType = "arraybuffer";
    socket.current = ws;
    const send = (message: object) => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify(message));

    // Ask for the size that fills the area; the server decides.
    const askToFit = () => {
      const size = fit.proposeDimensions();
      if (size && size.cols > 0 && size.rows > 0) send({ type: "resize", cols: size.cols, rows: size.rows });
    };
    const watchArea = new ResizeObserver(askToFit);

    let tookControl = false;
    ws.onopen = () => watchArea.observe(area.current!);
    ws.onmessage = (message) => {
      if (typeof message.data !== "string") {
        term.write(new Uint8Array(message.data));
        return;
      }
      const state = JSON.parse(message.data) as State;
      if (state.cols && state.rows && (state.cols !== term.cols || state.rows !== term.rows)) term.resize(state.cols, state.rows);
      const mine = state.controller === state.you;
      setSeat(mine ? "in control" : state.controller ? "someone else" : "watching");
      if (mine) term.focus();
      if (takeControlOnConnect && !tookControl && !state.controller) {
        tookControl = true;
        send({ type: "take" });
      }
    };
    ws.onclose = () => setSeat("ended");
    const encoder = new TextEncoder();
    const typing = term.onData((keys) => ws.readyState === WebSocket.OPEN && ws.send(encoder.encode(keys)));

    return () => {
      themeWatch.disconnect();
      watchArea.disconnect();
      typing.dispose();
      ws.onclose = null;
      ws.close();
      term.dispose();
    };
  }, [url, takeControlOnConnect]);

  const { tone, label } = SEATS[seat];
  const send = (type: "take" | "release") => socket.current?.send(JSON.stringify({ type }));
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-9 shrink-0 items-center gap-3 border-b border-divider bg-surface-1 px-3">
        <span className="font-mono text-[12px] font-semibold text-ink-primary">{title}</span>
        <span className="flex min-w-0 items-center gap-1.5 truncate text-[12px] text-ink-muted">
          <StatusDot tone={tone} />
          {label}
        </span>
        <span className="ml-auto" />
        {seat === "in control" ? (
          <Button size="sm" icon={<Undo2 />} onClick={() => send("release")}>
            Hand back
          </Button>
        ) : (
          <Button size="sm" icon={<Hand />} disabled={seat !== "watching"} onClick={() => send("take")}>
            Take control
          </Button>
        )}
      </div>
      <div ref={area} className="min-h-0 flex-1 scrollbar-thin overflow-auto bg-terminal p-2">
        <div ref={screen} className="h-full" />
      </div>
    </div>
  );
}
