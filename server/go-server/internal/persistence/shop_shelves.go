package persistence

import (
	"context"
	"strings"
	"time"
)

// These are read aliases, never new sale records. In particular 255 remains
// governed exclusively by offer_recommendations in Offers.
func compatibleShelfKinds(category, variant int) []byte {
	switch {
	case (category == 252 || category == 253) && variant == 25:
		return []byte{25}
	case category == 10 && variant == 67:
		return []byte{60, 31}
	case category == 67 && variant == 67:
		return []byte{64, 77, 31, 20, 21, 79, 74}
	case category == 19 && variant == 19:
		return []byte{20, 21}
	case category == 10 && variant == 30:
		return []byte{20, 21}
	}
	return nil
}

type shelfOffer struct {
	Offer
	enabled bool
}

func (m *ShopManager) compatibleOffers(category, variant int, kinds []byte) ([]Offer, error) {
	args := []any{category, variant}
	for _, kind := range kinds {
		args = append(args, kind)
	}
	// Include disabled rows so a deliberately disabled configured shelf is not
	// replaced by a fallback. The final result contains enabled offers only.
	query := `SELECT catalog_key,category,variant,record,grant_record,enabled FROM offers WHERE (category=? AND variant=?) OR (category=10 AND variant IN (` + strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",") + `)) ORDER BY catalog_key`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := m.store.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var source []shelfOffer
	for rows.Next() {
		var o shelfOffer
		if err := rows.Scan(&o.Key, &o.Category, &o.Variant, &o.Record, &o.Grant, &o.enabled); err != nil {
			return nil, err
		}
		if o.enabled && (len(o.Record) != 108 || len(o.Grant) != 68) {
			return nil, ErrDenied
		}
		source = append(source, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return arrangeCompatibleShelf(category, variant, kinds, source), nil
}

// Input is ordered by the original sale key. Preserve explicit shelf order,
// purchase IDs and bytes; only the response list's membership/order changes.
func arrangeCompatibleShelf(category, variant int, kinds []byte, source []shelfOffer) []Offer {
	var result []Offer
	configured := false
	existingItems := map[string]bool{}
	for _, o := range source {
		if int(o.Category) == category && int(o.Variant) == variant {
			configured = true
			if len(o.Grant) == 68 {
				existingItems[itemKey(o.Grant)] = true
			}
			if o.enabled {
				result = append(result, o.Offer)
			}
		}
	}
	appendDecor := category == 10 && variant == 30
	if configured && !appendDecor {
		return result[:min(len(result), 4000)]
	}
	byKind := map[byte][]Offer{}
	for _, o := range source {
		if !o.enabled || o.Category != 10 || (int(o.Category) == category && int(o.Variant) == variant) || len(o.Grant) != 68 {
			continue
		}
		if appendDecor && existingItems[itemKey(o.Grant)] {
			continue
		}
		if o.Grant[4] != o.Variant {
			continue
		}
		byKind[o.Variant] = append(byKind[o.Variant], o.Offer)
	}
	if category == 67 && variant == 67 {
		// Match the SD script's first three groups without inventing dummy
		// purchasable records. Sparse groups stay sparse; client visual paging
		// must be validated separately. Overflow remains in the response.
		for _, kind := range []byte{64, 77, 31} {
			n := min(16, len(byKind[kind]))
			result = append(result, byKind[kind][:n]...)
			byKind[kind] = byKind[kind][n:]
		}
		kinds = []byte{20, 21, 31, 79, 64, 77, 74}
	}
	for _, kind := range kinds {
		result = append(result, byKind[kind]...)
	}
	return result[:min(len(result), 4000)]
}
