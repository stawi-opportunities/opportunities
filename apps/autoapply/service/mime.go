package service

import "mime"

// mimeParseImpl is a function-typed seam so tests can override the
// stdlib parser in error-path tests without depending on header-quoting
// quirks.
var mimeParseImpl = mime.ParseMediaType
