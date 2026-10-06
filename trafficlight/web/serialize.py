from __future__ import annotations

import base64
import json
import math
from typing import TYPE_CHECKING, Any

from google.protobuf import descriptor, json_format, text_encoding, text_format
from google.protobuf.internal.type_checkers import ToShortestFloat

from trafficlight.proto_utils.proto_format import TYPES

if TYPE_CHECKING:
    from google.protobuf.message import Message as ProtobufMessage

    from trafficlight.proto_utils import Message, Proto

# top level enums of responses that tell whether a call worked, i.e. EncounterOutProto.status or FortSearchOutProto.result
STATUS_FIELDS = ("status", "result")


def proto_summary(proto: Proto) -> dict[str, Any]:
    """Everything the browser needs to list, filter and search a proto. Inspecting it loads proto_detail"""
    return {
        "method": proto.method_name,
        "value": proto.method_value,
        "status": get_status(proto),
        # a proxy's own payload is just the proxied proto again, no need to search it twice
        "request": _message_summary(proto.request, with_text=proto.proxy is None),
        "response": _message_summary(proto.response, with_text=proto.proxy is None),
        "proxy": proto_summary(proto.proxy) if proto.proxy else None,
    }


def proto_detail(proto: Proto) -> dict[str, Any]:
    return {
        "method": proto.method_name,
        "value": proto.method_value,
        "request": _message_detail(proto.request),
        "response": _message_detail(proto.response),
        "proxy": proto_detail(proto.proxy) if proto.proxy else None,
    }


def proto_export(proto: Proto) -> dict[str, Any]:
    # method, request and response are what mitms send, the rest is for humans
    return {
        "method": proto.method_value,
        "request": base64.b64encode(proto.request.decode_b64()).decode(),
        "response": base64.b64encode(proto.response.decode_b64()).decode(),
        "decoded": _decoded(proto),
    }


def get_status(proto: Proto) -> str | None:
    """Name of the response's status or result, i.e. ENCOUNTER_SUCCESS. A proxied response takes precedence"""
    for candidate in (proto.proxy, proto):
        if candidate is None:
            continue

        payload = candidate.response.payload
        if payload is None or not payload.ListFields():
            continue

        for field_name in STATUS_FIELDS:
            field = payload.DESCRIPTOR.fields_by_name.get(field_name)
            if field is None or field.enum_type is None or field.label == descriptor.FieldDescriptor.LABEL_REPEATED:
                continue

            number = getattr(payload, field_name)
            value = field.enum_type.values_by_number.get(number)
            return str(number) if value is None else value.name

    return None


def message_state(message: Message) -> str:
    if message.name is None:
        return "unknown"
    if message.payload is None:
        return "error"
    if not message.payload.ListFields():
        return "empty"
    return "ok"


def _message_summary(message: Message, with_text: bool) -> dict[str, Any]:
    state = message_state(message)
    return {
        "name": message.name,
        "state": state,
        "size": len(message.decode_b64()),
        "text": _text(message, one_line=True) if with_text and state != "empty" else "",
    }


def _message_detail(message: Message) -> dict[str, Any]:
    raw = message.decode_b64()
    return {
        "name": message.name,
        "state": message_state(message),
        "size": len(raw),
        "base64": base64.b64encode(raw).decode(),
        "text": _text(message, one_line=False),
        "json": _json(message),
        "tree": None if message.payload is None else _message_node(message.payload),
    }


def _decoded(proto: Proto) -> dict[str, Any]:
    return {
        "method": proto.method_name,
        "request": {"name": proto.request.name, "data": _json(proto.request)},
        "response": {"name": proto.response.name, "data": _json(proto.response)},
        "proxy": _decoded(proto.proxy) if proto.proxy else None,
    }


def _text(message: Message, one_line: bool) -> str:
    # unlike Message.to_string, this keeps non-ascii characters readable (and searchable)
    if message.payload is None:
        return json.dumps(message.blackbox, ensure_ascii=False, indent=None if one_line else 2)
    return text_format.MessageToString(message.payload, as_one_line=one_line, as_utf8=True)


def _json(message: Message) -> Any:
    if message.payload is None:
        return message.blackbox
    return json_format.MessageToDict(message.payload, preserving_proto_field_name=True)


# The tree keeps the type information the json loses. Every value is a node:
#   {"k": "msg", "t": type name, "f": [[field name, type name, is repeated, node or list of nodes], ...]}
#   {"k": "enum", "v": number, "e": name or None}, {"k": "bytes", "v": escaped, "n": length}
#   {"k": "str" | "num" | "bool", "v": value}


def _message_node(message: ProtobufMessage) -> dict[str, Any]:
    # message fields come last, like in the TUI
    fields = sorted(message.ListFields(), key=lambda f: f[0].cpp_type == descriptor.FieldDescriptor.CPPTYPE_MESSAGE)
    return {"k": "msg", "t": message.DESCRIPTOR.name, "f": [_field_entry(field, value) for field, value in fields]}


def _field_entry(field: descriptor.FieldDescriptor, value: Any) -> list[Any]:
    if _is_map_entry(field):
        entry = value.GetEntryClass()
        nodes = [_message_node(entry(key=key, value=value[key])) for key in sorted(value)]
        return [field.name, field.message_type.name, True, nodes]

    if field.label == descriptor.FieldDescriptor.LABEL_REPEATED:
        return [field.name, _type_name(field), True, [_value_node(field, item) for item in value]]

    return [field.name, _type_name(field), False, _value_node(field, value)]


def _value_node(field: descriptor.FieldDescriptor, value: Any) -> dict[str, Any]:
    cpp_type = field.cpp_type

    if cpp_type == descriptor.FieldDescriptor.CPPTYPE_MESSAGE:
        return _message_node(value)

    if cpp_type == descriptor.FieldDescriptor.CPPTYPE_ENUM:
        enum_value = field.enum_type.values_by_number.get(value)
        return {"k": "enum", "v": value, "e": None if enum_value is None else enum_value.name}

    if cpp_type == descriptor.FieldDescriptor.CPPTYPE_STRING:
        if field.type == descriptor.FieldDescriptor.TYPE_BYTES:
            return {"k": "bytes", "v": text_encoding.CEscape(value, False), "n": len(value)}
        return {"k": "str", "v": value}

    if cpp_type == descriptor.FieldDescriptor.CPPTYPE_BOOL:
        return {"k": "bool", "v": value}

    if cpp_type in (descriptor.FieldDescriptor.CPPTYPE_INT64, descriptor.FieldDescriptor.CPPTYPE_UINT64):
        # javascript numbers can't hold every 64 bit integer, i.e. encounter ids
        return {"k": "num", "v": str(value)}

    if cpp_type in (descriptor.FieldDescriptor.CPPTYPE_FLOAT, descriptor.FieldDescriptor.CPPTYPE_DOUBLE):
        if not math.isfinite(value):
            # nan and inf aren't valid json
            return {"k": "num", "v": str(value)}
        if cpp_type == descriptor.FieldDescriptor.CPPTYPE_FLOAT:
            # 0.9 instead of 0.8999999761581421
            value = ToShortestFloat(value)

    return {"k": "num", "v": value}


def _type_name(field: descriptor.FieldDescriptor) -> str:
    if field.message_type is not None:
        return field.message_type.name
    if field.enum_type is not None:
        return field.enum_type.name
    return TYPES.get(field.type, "unknown")


def _is_map_entry(field: descriptor.FieldDescriptor) -> bool:
    return (
        field.type == descriptor.FieldDescriptor.TYPE_MESSAGE
        and field.message_type.has_options
        and field.message_type.GetOptions().map_entry
    )
