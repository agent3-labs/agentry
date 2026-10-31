import type { FileViewProps } from "@/components/molecules/MonacoDiff";
import { CodeBlock } from "@/components/molecules/CodeBlock";
import { cn } from "@/lib/cn";

// The demo's file view (vite.demo.config.ts puts it in place of
// components/molecules/MonacoDiff): the file as plain text, and a changed file
// beside its last committed version, so the demo loads no Monaco. The two
// stack on a narrow screen and each scrolls inside itself.
export default function MonacoDiff({ now, before }: FileViewProps) {
  if (before === undefined) return <CodeBlock text={now} className="h-full rounded-none border-0" />;
  return (
    <div className="grid h-full grid-rows-2 md:grid-cols-2 md:grid-rows-1">
      <Pane label="Last committed" text={before} className="border-b border-divider md:border-r md:border-b-0" />
      <Pane label="Now" text={now} />
    </div>
  );
}

function Pane({ label, text, className }: { label: string; text: string; className?: string }) {
  return (
    <div className={cn("flex min-h-0 min-w-0 flex-col", className)}>
      <div className="border-b border-divider px-3 py-1 text-xs text-ink-muted">{label}</div>
      <CodeBlock text={text} className="min-h-0 flex-1 rounded-none border-0" />
    </div>
  );
}
