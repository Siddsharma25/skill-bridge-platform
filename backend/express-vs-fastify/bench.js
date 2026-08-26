// Drives the identical POST /echo request at both servers for a fixed
// duration and prints real numbers side by side — the actual point of
// this comparison being runnable rather than just asserted. Run
// `npm run express` and `npm run fastify` in two other terminals first
// (or start them however you like), then `npm run bench`.
const autocannon = require("autocannon");

const body = JSON.stringify({ message: "hello from the bench script", priority: 3 });
const headers = { "content-type": "application/json" };

function run(name, url) {
  return new Promise((resolve, reject) => {
    autocannon(
      {
        url,
        method: "POST",
        body,
        headers,
        connections: 50,
        duration: 10,
      },
      (err, result) => {
        if (err) return reject(err);
        resolve({ name, result });
      },
    );
  });
}

async function main() {
  const targets = [
    { name: "express", url: process.env.EXPRESS_URL || "http://localhost:3001/echo" },
    { name: "fastify", url: process.env.FASTIFY_URL || "http://localhost:3002/echo" },
  ];

  for (const t of targets) {
    const { name, result } = await run(t.name, t.url);
    console.log(`\n=== ${name} ===`);
    console.log(`requests/sec (avg): ${result.requests.average}`);
    console.log(`latency (avg, ms):  ${result.latency.average}`);
    console.log(`throughput (avg):   ${(result.throughput.average / 1024 / 1024).toFixed(2)} MB/s`);
    console.log(`errors:             ${result.errors}`);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
