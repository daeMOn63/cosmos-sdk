package ormdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"sort"

	"github.com/cosmos/cosmos-sdk/orm/encoding/encodeutil"

	"github.com/cosmos/cosmos-sdk/orm/encoding/ormkv"
	"github.com/cosmos/cosmos-sdk/orm/types/ormerrors"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/cosmos/cosmos-sdk/orm/model/ormtable"
)

type FileDescriptorDBOptions struct {
	Prefix []byte
	ID     uint32

	// TypeResolver is an optional type resolver to be used when unmarshaling
	// protobuf messages.
	TypeResolver ormtable.TypeResolver

	// JSONValidator is an optional validator that can be used for validating
	// messaging when using ValidateJSON. If it is nil, DefaultJSONValidator
	// will be used
	JSONValidator func(proto.Message) error

	GetBackend func(context.Context) (ormtable.Backend, error)

	GetReadBackend func(context.Context) (ormtable.ReadBackend, error)
}

type FileDescriptorDB struct {
	id             uint32
	prefix         []byte
	tablesById     map[uint32]ormtable.Table
	tablesByName   map[protoreflect.FullName]ormtable.Table
	fileDescriptor protoreflect.FileDescriptor
}

func NewFileDescriptorSchema(fileDescriptor protoreflect.FileDescriptor, options FileDescriptorDBOptions) (*FileDescriptorDB, error) {
	prefix := encodeutil.AppendVarUInt32(options.Prefix, options.ID)

	schema := &FileDescriptorDB{
		id:             options.ID,
		prefix:         prefix,
		tablesById:     map[uint32]ormtable.Table{},
		tablesByName:   map[protoreflect.FullName]ormtable.Table{},
		fileDescriptor: fileDescriptor,
	}

	messages := fileDescriptor.Messages()
	n := messages.Len()
	for i := 0; i < n; i++ {
		messageDescriptor := messages.Get(i)
		messageType, err := options.TypeResolver.FindMessageByName(messageDescriptor.FullName())
		if err != nil {
			return nil, err
		}

		table, err := ormtable.Build(ormtable.Options{
			Prefix:         prefix,
			MessageType:    messageType,
			TypeResolver:   options.TypeResolver,
			JSONValidator:  options.JSONValidator,
			GetReadBackend: options.GetReadBackend,
			GetBackend:     options.GetBackend,
		})
		if err != nil {
			return nil, err
		}

		schema.tablesByName[messageDescriptor.FullName()] = table
		schema.tablesById[table.ID()] = table
	}

	return schema, nil
}

func (f FileDescriptorDB) DecodeEntry(k, v []byte) (ormkv.Entry, error) {
	r := bytes.NewReader(k)
	err := encodeutil.SkipPrefix(r, f.prefix)
	if err != nil {
		return nil, err
	}

	id, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}

	if id > math.MaxUint32 {
		return nil, ormerrors.UnexpectedDecodePrefix.Wrapf("uint32 varint id out of range %d", id)
	}

	table, ok := f.tablesById[uint32(id)]
	if !ok {
		return nil, ormerrors.UnexpectedDecodePrefix.Wrapf("can't find table with id %d", id)
	}

	return table.DecodeEntry(k, v)
}

func (f FileDescriptorDB) EncodeEntry(entry ormkv.Entry) (k, v []byte, err error) {
	table, ok := f.tablesByName[entry.GetTableName()]
	if !ok {
		return nil, nil, ormerrors.BadDecodeEntry.Wrapf("can't find table %s", entry.GetTableName())
	}

	return table.EncodeEntry(entry)
}

func (f FileDescriptorDB) GetTable(message proto.Message) ormtable.Table {
	table, _ := f.tablesByName[message.ProtoReflect().Descriptor().FullName()]
	return table
}

func (f FileDescriptorDB) AutoMigrate(ctx context.Context) error {
	var sortedIds []int
	for id := range f.tablesById {
		sortedIds = append(sortedIds, int(id))
	}
	sort.Ints(sortedIds)

	for _, id := range sortedIds {
		id := uint32(id)
		err := f.tablesById[id].AutoMigrate(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

var _ ormkv.EntryCodec = FileDescriptorDB{}