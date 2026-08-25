# Multi-stage build for notification-service. Three stages: install +
# build with dev dependencies (TypeScript, Nest CLI), a second install
# pass for production-only dependencies, then a slim runtime image that
# only ever sees compiled JS + prod node_modules — mirrors the Go
# Dockerfiles' "final image doesn't carry the toolchain that built it"
# shape (see backend/Dockerfile.auth-service), adapted to what a Node
# service actually needs at runtime (unlike a static Go binary, this one
# still needs a JS runtime and node_modules).
FROM node:22-alpine AS build
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json tsconfig.build.json nest-cli.json ./
COPY src ./src
RUN npm run build

# Separate production-only install, rather than pruning the build stage's
# node_modules, keeps this step independent/cacheable and guarantees no
# devDependency (ts-node, jest, eslint, ...) leaks into the final image.
FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev

# node:22-alpine (not a distroless/nonroot image, unlike the Go services)
# because this service genuinely needs a shell-less-but-still-a-runtime
# Node environment; alpine keeps it small while staying inside the
# well-supported Node Docker images rather than hand-rolling a distroless
# Node base. Runs as the image's built-in unprivileged `node` user rather
# than root, matching the Go side's non-root convention even though the
# base image differs.
FROM node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY --from=deps /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY package.json ./
USER node

# HTTP health-check port only (see .env.example) — this service's real
# work (consuming notifications.email) happens over its RabbitMQ
# connection, not an exposed port.
EXPOSE 8085

ENTRYPOINT ["node", "dist/main.js"]
