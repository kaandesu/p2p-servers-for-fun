FROM golang:1.22-alpine

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

RUN go build -o server .

EXPOSE 3000 3001

CMD ["./server", "--apex"]
