const BLOBS = [
  { color: "#ec4899", top: "-8%", left: "-6%", size: 520, delay: "0s" },
  { color: "#8b5cf6", top: "-4%", left: "26%", size: 460, delay: "1.5s" },
  { color: "#0ea5e9", top: "2%", left: "66%", size: 500, delay: "3s" },
  { color: "#10b981", top: "32%", left: "-10%", size: 440, delay: "2s" },
  { color: "#f59e0b", top: "38%", left: "48%", size: 480, delay: "0.8s" },
  { color: "#f43f5e", top: "68%", left: "8%", size: 420, delay: "2.6s" },
  { color: "#06b6d4", top: "62%", left: "78%", size: 440, delay: "1.2s" },
  { color: "#a3e635", top: "88%", left: "38%", size: 380, delay: "3.4s" },
];

export function PaintSplashBackground() {
  return (
    <div aria-hidden className="pointer-events-none fixed inset-0 -z-10 overflow-hidden bg-background">
      {BLOBS.map((b, i) => (
        <div
          key={i}
          className="animate-blob absolute rounded-full opacity-70 blur-xl dark:opacity-50 dark:mix-blend-plus-lighter"
          style={{
            top: b.top,
            left: b.left,
            width: b.size,
            height: b.size,
            background: b.color,
            animationDelay: b.delay,
          }}
        />
      ))}
    </div>
  );
}
