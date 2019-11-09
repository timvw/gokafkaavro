package gokafkaavro

import (
	"encoding/binary"
	"errors"
	"fmt"
	schemaregistry "github.com/lensesio/schema-registry"
	"github.com/linkedin/goavro"
)

type SubjectName = string
type AvroSchema = string
type SubjectVersion = int

type SubjectNameStrategy interface {
	GetSubjectName(topic string, isKey bool)(subjectName SubjectName)
}

type Decoder struct {
	client schemaregistry.Client
}

type Encoder struct {
	subjectVersion SubjectVersion
	codec goavro.Codec
}

func NewEncoder(client schemaregistry.Client, autoRegister bool, subjectName SubjectName, avroSchema AvroSchema)(encoder Encoder, err error) {

	var subjectVersion SubjectVersion

	if(autoRegister) {

		subjectVersion, err = client.RegisterNewSchema(subjectName, avroSchema)

		if err != nil {
			return
		}
	} else {
		isRegistered, schema, clientErr := client.IsRegistered(subjectName, avroSchema)

		if clientErr != nil {
			err = clientErr
			return
		}

		if !isRegistered {
			err = errors.New(fmt.Sprintf("There is no registration on subject %v for schema %v", subjectName, avroSchema))
			return
		}

		subjectVersion = schema.Version
	}

	codec, codecErr := goavro.NewCodec(avroSchema)

	if codecErr != nil {
		err = codecErr
		return
	}

	encoder = Encoder{ subjectVersion, *codec}

	return
}

func (e Encoder) Encode(native interface{})(avroBytes []byte, err error) {
	avroBytes, err = e.codec.BinaryFromNative(nil, native)
	return
}


// Codec decodes kafka avro messages using a schema registry
type Codec struct {
	client              schemaRegistryClient
	codecCache          map[subjectVersionID]*goavro.Codec
	subjectNameStrategy func(topic string, isKey bool)(string)
}

// NewCodec returns a new instance of Codec
func NewCodec(client schemaRegistryClient) (*Codec) {
	return &Codec{client, make(map[subjectVersionID]*goavro.Codec), getTopicNameStrategy}
}

// Decode returns a native datum value for the binary encoded byte slice
// in accordance with the Avro schema attached to the data
// [wire-format](https://docs.confluent.io/current/schema-registry/serializer-formatter.html#wire-format).
// On success, it returns the decoded datum and a nil error value.
// On error, it returns nil for the datum value and the error message.
func (c *Codec) Decode(topic string, isKey bool, data []byte) (native interface{}, err error) {

	subjectVersion, err := extractSubjectAndVersionFromData(topic, isKey, data)
	if err != nil {
		return
	}

	codec, err := c.getCodecFor(subjectVersion)
	if err != nil {
		return
	}

	native, _, err = codec.NativeFromBinary(data[5:])
	return
}

func (c *Codec) Encode(topic string, isKey bool, schema string, native interface{}) (data []byte, err error) {

	subject := getTopicNameStrategy(topic, isKey)
	versionID, err := c.client.GetVersionFor(subject, schema)

	if err != nil {
		return
	}

	subjectVersionID := subjectVersionID{ subject,versionID}

	codec, err := c.getCodecFor(subjectVersionID)
	if err != nil {
		return
	}

	magicByte := []byte{0}
	versionBytes := bytesForSchemaID(subjectVersionID.versionID)

	dataBytes, err := codec.BinaryFromNative(nil, native)
	if err != nil {
		return
	}

	data = append(append(magicByte, versionBytes...), dataBytes...)

	return
}


type subjectVersionID struct {
	subject   string
	versionID int
}

func extractSubjectAndVersionFromData(topic string, isKey bool, data []byte) (key subjectVersionID, err error) {

	magicByte := data[0]

	if magicByte != 0 {
		err = errors.New("Unknown magic byte")
		return
	}

	subject := getTopicNameStrategy(topic, isKey)
	versionID := getSchemaID(data[1:5])
	key = subjectVersionID{subject, versionID}
	return
}

func getTopicNameStrategy(topic string, isKey bool) (subject string) {
	if isKey {
		return fmt.Sprintf("%v-key", topic)
	}

	return fmt.Sprintf("%v-value", topic)
}

func getSchemaID(data []byte) int {
	return int(binary.BigEndian.Uint32(data))
}

func bytesForSchemaID(schemaID int) (data []byte) {
	data = make([]byte, 4)
	binary.BigEndian.PutUint32(data, uint32(schemaID))
	return
}

func (c *Codec) getCodecFor(subjectVersion subjectVersionID) (codec *goavro.Codec, err error) {

	codec, ok := c.codecCache[subjectVersion]

	if !ok {
		var schema string
		schema, err = c.client.GetSchemaFor(subjectVersion)
		if err != nil {
			return
		}
		codec, err = goavro.NewCodec(schema)
		if err != nil {
			return
		}
		c.codecCache[subjectVersion] = codec
	}

	return
}

