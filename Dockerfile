FROM golang:1.9
WORKDIR /go/src/app
COPY prerender.go .
RUN apt-get update && apt-get upgrade -y && \
    apt-get install -y git
RUN go-wrapper download   # "go get -d -v ./..."
RUN go-wrapper install    # "go install -v ./..."
CMD ["go-wrapper", "run"] # ["app"]
