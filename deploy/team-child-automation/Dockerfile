FROM node:22-bookworm-slim

WORKDIR /app
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
# The service connects to the separately managed browser over CDP.
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY server.mjs ./server.mjs
COPY totp.mjs ./totp.mjs
COPY batch-oauth.mjs ./batch-oauth.mjs
COPY email-code.mjs ./email-code.mjs
COPY browser-proxy.mjs ./browser-proxy.mjs

ENV NODE_ENV=production
EXPOSE 8090
CMD ["node", "server.mjs"]
