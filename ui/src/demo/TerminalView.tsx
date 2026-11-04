import type { ComponentProps } from "react";
import { Hand } from "lucide-react";
import { Button } from "@/components/atoms/Button";
import { StatusDot } from "@/components/atoms/StatusDot";
import type { TerminalView as LiveTerminal } from "@/components/organisms/TerminalView";
import { cn } from "@/lib/cn";
import { LINES } from "./terminal";

// The demo's terminal (vite.demo.config.ts puts it in place of
// components/organisms/TerminalView): a few read-only lines in the
// terminal's colours, without xterm. Nobody can take control.
export function TerminalView({ title }: ComponentProps<typeof LiveTerminal>) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-9 shrink-0 items-center gap-3 border-b border-divider bg-surface-1 px-3">
        <span className="font-mono text-[12px] font-semibold text-ink-primary">{title}</span>
        <span className="flex min-w-0 items-center gap-1.5 truncate text-[12px] text-ink-muted">
          <StatusDot tone="success" />
          Watching
        </span>
        <span className="ml-auto" />
        <Button size="sm" icon={<Hand />} disabled>
          Take control
        </Button>
      </div>
      <pre className="min-h-0 flex-1 scrollbar-thin overflow-auto bg-terminal p-2 font-mono text-[13px] leading-[1.2] text-ink-primary">
        {LINES.map(({ text, tone }, i) => (
          <div key={i} className={cn(tone === "bold" && "font-semibold", tone === "dim" && "text-ink-muted")}>
            {text || " "}
          </div>
        ))}
      </pre>
    </div>
  );
}
