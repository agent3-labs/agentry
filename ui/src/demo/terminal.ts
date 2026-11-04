// The lines a terminal of the demo shows in place of an agent's session.

export const LINES: { text: string; tone?: "bold" | "dim" }[] = [
  { text: "Agentry demo", tone: "bold" },
  { text: "" },
  { text: "In Agentry, this is the agent's own terminal, live: you watch it work," },
  { text: "and Take control lets you type in it." },
  { text: "" },
  { text: "Nothing runs in the demo. Download Agentry to use it.", tone: "dim" },
];
