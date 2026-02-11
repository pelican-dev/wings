package parser

import (
	"regexp"
	"strconv"
	"strings"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/buger/jsonparser"
	"github.com/iancoleman/strcase"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Regex to match anything that has a value matching the format of {{ config.$1 }} which
// will cause the program to lookup that configuration value from itself and set that
// value to the configuration one.
//
// This allows configurations to reference values that are node dependent, such as the
// internal IP address used by the daemon, useful in Bungeecord setups for example, where
// it is common to see variables such as "{{config.docker.interface}}"
var configMatchRegex = regexp.MustCompile(`{{\s?config\.([\w.-]+)\s?}}`)

// Regex to support modifying XML inline variable data using the config tools. This means
// you can pass a replacement of Root.Property='[value="testing"]' to get an XML node
// matching:
//
// <Root>
//
//	<Property value="testing"/>
//
// </Root>
//
// noinspection RegExpRedundantEscape
var xmlValueMatchRegex = regexp.MustCompile(`^\[([\w]+)='(.*)'\]$`)

// Gets the value of a key based on the value type defined.
func (cfr *ConfigurationFileReplacement) getKeyValue(value string) interface{} {
	if cfr.ReplaceWith.Type() == jsonparser.Boolean {
		v, _ := strconv.ParseBool(value)
		return v
	}

	// Try to parse into an int, if this fails just ignore the error and continue
	// through, returning the string.
	if v, err := strconv.Atoi(value); err == nil {
		return v
	}

	return value
}

// Iterate over an unstructured JSON/YAML/etc. interface and set all of the required
// key/value pairs for the configuration file.
//
// We need to support wildcard characters in key searches, this allows you to make
// modifications to multiple keys at once, especially useful for games with multiple
// configurations per-world (such as Spigot and Bungeecord) where we'll need to make
// adjustments to the bind address for the user.
//
// This does not currently support nested wildcard matches. For example, foo.*.bar
// will work, however foo.*.bar.*.baz will not, since we'll only be splitting at the
// first wildcard, and not subsequent ones.
func (f *ConfigurationFile) IterateOverJson(data []byte) ([]byte, error) {
	if !gjson.ValidBytes(data) {
		return nil, errors.New("invalid JSON data")
	}

	jsonStr := string(data)

	for _, v := range f.Replace {
		value, err := f.LookupConfigurationValue(v)
		if err != nil {
			return nil, err
		}

		// Check for a wildcard character, and if found split the key on that value to
		// begin doing a search and replace in the data.
		if strings.Contains(v.Match, ".*") {
			parts := strings.SplitN(v.Match, ".*", 2)
			basePath := strings.Trim(parts[0], ".")
			remainingPath := strings.Trim(parts[1], ".")

			result := gjson.Get(jsonStr, basePath)
			if !result.Exists() {
				continue
			}

			if result.IsArray() {
				result.ForEach(func(key, val gjson.Result) bool {
					fullPath := basePath + "." + key.String()
					if remainingPath != "" {
						fullPath += "." + remainingPath
					}
					var setErr error
					jsonStr, setErr = v.setValueWithSjson(jsonStr, fullPath, value)
					if setErr != nil {
						err = setErr
						return false
					}
					return true
				})
				if err != nil {
					return nil, errors.WithMessage(err, "failed to set config value of array child")
				}
			} else if result.IsObject() {
				result.ForEach(func(key, val gjson.Result) bool {
					fullPath := basePath + "." + key.String()
					if remainingPath != "" {
						fullPath += "." + remainingPath
					}
					var setErr error
					jsonStr, setErr = v.setValueWithSjson(jsonStr, fullPath, value)
					if setErr != nil {
						err = setErr
						return false
					}
					return true
				})
				if err != nil {
					return nil, errors.WithMessage(err, "failed to set config value of object child")
				}
			}
			continue
		}

		var setErr error
		jsonStr, setErr = v.setValueWithSjson(jsonStr, v.Match, value)
		if setErr != nil {
			if strings.Contains(setErr.Error(), "path not found") {
				continue
			}
			return nil, errors.WithMessage(setErr, "unable to set config value at pathway: "+v.Match)
		}
	}

	return []byte(jsonStr), nil
}

func (cfr *ConfigurationFileReplacement) setValueWithSjson(jsonStr string, path string, value string) (string, error) {
	if cfr.IfValue != "" {
		// Check if we are replacing instead of overwriting.
		if strings.HasPrefix(cfr.IfValue, "regex:") {
			result := gjson.Get(jsonStr, path)
			if !result.Exists() {
				return jsonStr, nil
			}

			r, err := regexp.Compile(strings.TrimPrefix(cfr.IfValue, "regex:"))
			if err != nil {
				log.WithFields(log.Fields{"if_value": strings.TrimPrefix(cfr.IfValue, "regex:"), "error": err}).
					Warn("configuration if_value using invalid regexp, cannot perform replacement")
				return jsonStr, nil
			}

			v := result.String()
			if r.MatchString(v) {
				newValue := r.ReplaceAllString(v, value)
				return sjson.Set(jsonStr, path, newValue)
			}
			return jsonStr, nil
		}

		result := gjson.Get(jsonStr, path)
		if !result.Exists() {
			return jsonStr, nil
		}
		if result.String() != cfr.IfValue {
			return jsonStr, nil
		}
	}

	var setValue interface{}
	if cfr.ReplaceWith.Type() == jsonparser.Boolean {
		v, _ := strconv.ParseBool(value)
		setValue = v
	} else if v, err := strconv.Atoi(value); err == nil {
		setValue = v
	} else {
		setValue = value
	}

	return sjson.Set(jsonStr, path, setValue)
}

// Looks up a configuration value on the Daemon given a dot-notated syntax.
func (f *ConfigurationFile) LookupConfigurationValue(cfr ConfigurationFileReplacement) (string, error) {
	// If this is not something that we can do a regex lookup on then just continue
	// on our merry way. If the value isn't a string, we're not going to be doing anything
	// with it anyways.
	if cfr.ReplaceWith.Type() != jsonparser.String || !configMatchRegex.Match(cfr.ReplaceWith.Value()) {
		return cfr.ReplaceWith.String(), nil
	}

	// If there is a match, lookup the value in the configuration for the Daemon. If no key
	// is found, just return the string representation, otherwise use the value from the
	// daemon configuration here.
	huntPath := configMatchRegex.ReplaceAllString(
		configMatchRegex.FindString(cfr.ReplaceWith.String()), "$1",
	)

	var path []string
	for _, value := range strings.Split(huntPath, ".") {
		path = append(path, strcase.ToSnake(value))
	}

	// Look for the key in the configuration file, and if found return that value to the
	// calling function.
	match, _, _, err := jsonparser.Get(f.configuration, path...)
	if err != nil {
		if err != jsonparser.KeyPathNotFoundError {
			return string(match), err
		}

		log.WithFields(log.Fields{"path": path, "filename": f.FileName}).Debug("attempted to load a configuration value that does not exist")

		// If there is no key, keep the original value intact, that way it is obvious there
		// is a replace issue at play.
		return string(match), nil
	} else {
		return configMatchRegex.ReplaceAllString(cfr.ReplaceWith.String(), string(match)), nil
	}
}
