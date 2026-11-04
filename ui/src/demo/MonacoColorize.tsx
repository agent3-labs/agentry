// The demo's code colouring (vite.demo.config.ts puts it in place of
// components/molecules/MonacoColorize): the code stays plain, so the demo
// loads no Monaco.
export default function MonacoColorize({ code }: { code: string; language: string }) {
  return <>{code}</>;
}
