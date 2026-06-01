FROM alpine:3.20
RUN apk add --no-cache ffmpeg
COPY sweep.sh loop.sh /usr/local/bin/
RUN chmod +x /usr/local/bin/sweep.sh /usr/local/bin/loop.sh
ENTRYPOINT ["/usr/local/bin/loop.sh"]
