FROM mcr.microsoft.com/playwright:v1.55.1-noble

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY server.mjs ./server.mjs
COPY totp.mjs ./totp.mjs
COPY batch-oauth.mjs ./batch-oauth.mjs
COPY browser-proxy.mjs ./browser-proxy.mjs

ENV NODE_ENV=production
EXPOSE 8090
CMD ["node", "server.mjs"]
