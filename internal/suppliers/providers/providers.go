// Package providers contains all supplier provider implementations.
// Import this package to register all providers with the global registry.
package providers

import (
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/altronics"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/amazon"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/arrow"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/avnet"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/bunnings"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/component-distributor"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/core-electronics"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/datasheet"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/digikey"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/element14"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/farnell"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/future-electronics"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/heilind"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/jaycar"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/lcsc"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/littlebird"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/mouser"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/rs-components"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/rutronik"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/semikron"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers/tme"
)
