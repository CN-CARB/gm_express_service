FROM denoland/deno:alpine
USER root
RUN apk add --no-cache iproute2
WORKDIR /app
COPY index.js /app/index.js
COPY docker/selfhost.js /app/setup_app.js
RUN deno cache /app/index.js
ENTRYPOINT ["/bin/sh", "-ec"]
CMD ["tc qdisc add dev eth0 root tbf rate ${BENCH_RATE:-10mbit} burst 16kb latency 500ms; exec deno run --cached-only --allow-net --allow-read --allow-env /app/index.js"]
