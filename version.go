// Package pluginprotocol exposes the public version of the Liapoldus plugin
// protocol module. Wire compatibility is scoped to the major protocol version.
package pluginprotocol

// ProtocolVersion is the stable protocol contract version implemented by this
// module. It is intentionally independent from the Go module release tag.
const ProtocolVersion = "liapoldus.plugin.v1"
