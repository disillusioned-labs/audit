package kafka

import "github.com/twmb/franz-go/pkg/kgo"

// Record is the application-facing Kafka record.
//
// EventID is infrastructure metadata and is therefore transported as a Kafka
// header rather than being forced into every event payload.
type Record struct {
	Topic   string
	Key     []byte
	Value   []byte
	Headers []kgo.RecordHeader
}
