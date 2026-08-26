// The Fastify half of this comparison. Same two routes as
// express-server.js. The /echo route declares a JSON Schema instead of
// hand-writing checks — Fastify validates the request against it (a 400
// with a machine-generated error is returned automatically on a bad
// body, before the handler even runs) and uses the *response* schema to
// pre-compile a fast serializer for the reply, which is the concrete
// mechanism behind Fastify's serialization speed advantage (see
// README.md) — not just "the framework is leaner," but this specific
// schema-to-serializer compilation step Express has no equivalent of.
const fastify = require("fastify")({ logger: false });

fastify.get("/health", async () => {
  return { status: "ok" };
});

const echoSchema = {
  schema: {
    body: {
      type: "object",
      required: ["message", "priority"],
      properties: {
        message: { type: "string", minLength: 1 },
        priority: { type: "integer", minimum: 1, maximum: 5 },
      },
    },
    response: {
      200: {
        type: "object",
        properties: {
          message: { type: "string" },
          priority: { type: "integer" },
          receivedAt: { type: "string" },
        },
      },
    },
  },
};

fastify.post("/echo", echoSchema, async (request) => {
  const { message, priority } = request.body;
  return { message, priority, receivedAt: new Date().toISOString() };
});

const port = process.env.PORT || 3002;
fastify.listen({ port }, (err) => {
  if (err) {
    console.error(err);
    process.exit(1);
  }
  console.log(`fastify-server listening on :${port}`);
});
