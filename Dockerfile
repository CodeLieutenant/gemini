FROM busybox AS production

WORKDIR /gemini

COPY gemini .

ENV PATH="/gemini:${PATH}"

ENTRYPOINT ["gemini"]
