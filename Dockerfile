# Release image, built by GoReleaser (dockers_v2) from the binaries it compiled. The build context
# holds one binary per platform under $TARGETPLATFORM/rowbird. To build an image by hand, run
# `make snapshot` instead of `docker build`.

# distroless has no shell, so /data is created here with the right owner and copied over.
FROM busybox:1.37 AS data
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY --from=data --chown=65532:65532 /data /data
COPY $TARGETPLATFORM/rowbird /rowbird
USER 65532:65532
ENV ROWBIRD_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["/rowbird", "healthcheck"]
ENTRYPOINT ["/rowbird"]
CMD ["serve"]
