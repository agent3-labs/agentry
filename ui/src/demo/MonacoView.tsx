import { TextArea } from "@/components/atoms/Input";
import type { CodeViewerProps } from "@/components/molecules/CodeViewer";
import { CodeBlock } from "@/components/molecules/CodeBlock";

// The demo's editor (vite.demo.config.ts puts it in place of
// components/molecules/MonacoView): the plain text CodeViewer shows until
// Monaco loads, so the demo loads no Monaco.
export default function MonacoView({ text, onChange, readOnly, placeholder }: CodeViewerProps) {
  if (!onChange) return <CodeBlock text={text} className="border-0 bg-transparent" />;
  return (
    <TextArea
      value={text}
      onChange={(event) => onChange(event.target.value)}
      readOnly={readOnly}
      placeholder={placeholder}
      rows={16}
      className="font-mono text-[12.5px]"
    />
  );
}
