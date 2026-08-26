// The Express half of this comparison. Same two routes as
// fastify-server.js, deliberately implemented the way a real Express app
// would: no schema library bolted on (that's exactly the contrast this
// comparison is making — Fastify has this built in, Express doesn't).
const express = require("express");

const app = express();
app.use(express.json());

app.get("/health", (_req, res) => {
  res.json({ status: "ok" });
});

// Manual validation — every check written and maintained by hand. This
// is the normal Express pattern (or you reach for a separate library like
// ajv/joi/zod + a validation middleware) — there's no route-level schema
// concept in Express itself.
app.post("/echo", (req, res) => {
  const { message, priority } = req.body ?? {};

  if (typeof message !== "string" || message.length === 0) {
    return res.status(400).json({ error: "message must be a non-empty string" });
  }
  if (typeof priority !== "number" || !Number.isInteger(priority) || priority < 1 || priority > 5) {
    return res.status(400).json({ error: "priority must be an integer between 1 and 5" });
  }

  res.json({ message, priority, receivedAt: new Date().toISOString() });
});

const port = process.env.PORT || 3001;
app.listen(port, () => {
  console.log(`express-server listening on :${port}`);
});
