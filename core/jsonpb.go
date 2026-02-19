package core

import (
	"bytes"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
)

var jsonpbMarshaler = &jsonpb.Marshaler{EmitDefaults: true}

// JSONToMessage converts JSON request body to a dynamic.Message of the method's input type (compatible with grpcdynamic proto.Message).
func JSONToMessage(method *desc.MethodDescriptor, jsonBody []byte) (proto.Message, error) {
	msgDesc := method.GetInputType()
	msg := dynamic.NewMessage(msgDesc)
	if err := jsonpb.Unmarshal(bytes.NewReader(jsonBody), msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// MessageToJSON converts gRPC response proto message to JSON.
func MessageToJSON(msg proto.Message) ([]byte, error) {
	var buf bytes.Buffer
	if err := jsonpbMarshaler.Marshal(&buf, msg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
