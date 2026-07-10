package fieldauth

// resolveRoles maps an authenticated request onto governance roles.
// Persona tokens minted by the policy service carry a "roles" claim
// directly; Google/Firebase-signed tokens carry none, so their identity
// (email, then sub) is looked up in the bundle's principals map — keeping
// the policy service the system of record for identity→role mapping.
func resolveRoles(claims map[string]any, principals map[string][]string) []string {
	if roles := stringList(claims["roles"]); len(roles) > 0 {
		return roles
	}
	for _, key := range []string{"email", "sub"} {
		if subject, ok := claims[key].(string); ok && subject != "" {
			if roles, ok := principals[subject]; ok {
				return roles
			}
		}
	}
	return nil
}

func stringList(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	default:
		return nil
	}
}

// deniedCoordinates returns the selected coordinates the given roles may not
// read. Coordinates absent from the bundle are ungoverned and always allowed
// (allow-unless-governed); governed coordinates require at least one shared
// role, so an empty allowedRoles list denies everyone.
func deniedCoordinates(coordinates []string, bundle *Bundle, roles []string) []string {
	roleSet := make(map[string]bool, len(roles))
	for _, role := range roles {
		roleSet[role] = true
	}
	var denied []string
	for _, coordinate := range coordinates {
		entry, governed := bundle.Fields[coordinate]
		if !governed {
			continue
		}
		allowed := false
		for _, role := range entry.AllowedRoles {
			if roleSet[role] {
				allowed = true
				break
			}
		}
		if !allowed {
			denied = append(denied, coordinate)
		}
	}
	return denied
}
