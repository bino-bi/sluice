// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

import "strings"

// The lists below were reviewed against duckdb_functions() of the embedded
// DuckDB (scripts/DUCKDB_VERSION). A function is listed only if it computes
// over its arguments (plus clock, random and uuid). Left out on purpose:
// settings, variables and environment (current_setting, getvariable),
// session and catalog introspection (current_database, current_schema,
// current_query, version, everything in pg_catalog), sequences (nextval,
// currval), column statistics (stats), type metadata (typeof and enum_range
// return the labels of an ENUM type, not values of visible rows), COLUMNS()
// star expressions, functions that call another function by a string name
// (list_aggregate), anything with side effects (setseed, sleep_ms,
// write_log), internal helpers and every extension function. A name missing
// here is rejected, so a new DuckDB release cannot add an unreviewed
// function.

// tableFunctions may appear in FROM. Their arguments must not contain
// column references or subqueries.
var tableFunctions = nameSet(`generate_series range unnest`)

// syntaxFunctions are names that do not exist in duckdb_functions(): what
// pg_query emits for SQL syntax forms (EXTRACT, SUBSTRING ... FROM, TRIM,
// POSITION, AT TIME ZONE, LIKE ... ESCAPE, SIMILAR TO, CURRENT_TIME, ...)
// and names DuckDB resolves in its binder.
var syntaxFunctions = nameSet(`
	extract btrim similar_to_escape
	current_time current_timestamp localtime localtimestamp
	if ifnull try unnest percentile_cont percentile_disc date grouping_id
`)

// exprFunctions may appear anywhere outside FROM.
var exprFunctions = nameSet(
	// Aggregates and window functions.
	`any_value approx_count_distinct approx_quantile approx_top_k arbitrary
	arg_max argmax arg_max_null arg_max_nulls_last arg_min argmin arg_min_null
	arg_min_nulls_last array_agg avg bit_and bit_or bitstring_agg bit_xor
	bool_and bool_or corr count count_if countif count_star covar_pop
	covar_samp cume_dist dense_rank entropy favg fill first first_value fsum
	geomean geometric_mean group_concat histogram histogram_exact kahan_sum
	kurtosis kurtosis_pop lag last last_value lead list listagg mad max max_by
	mean median min min_by mode nth_value ntile percent_rank product quantile
	quantile_cont quantile_disc rank rank_dense regr_avgx regr_avgy regr_count
	regr_intercept regr_r2 regr_slope regr_sxx regr_sxy regr_syy
	reservoir_quantile row_number sem skewness stddev stddev_pop stddev_samp
	string_agg sum sumkahan sum_no_overflow variance var_pop var_samp wavg
	weighted_avg`,

	// Math and bit operations.
	`abs acos acosh add asin asinh atan atan2 atanh bit_count cbrt ceil
	ceiling cos cosh cot degrees divide equi_width_bins even exp factorial
	fdiv floor fmod gamma gcd greatest greatest_common_divisor
	is_histogram_other_bin isfinite isinf isnan lcm least
	least_common_multiple lgamma ln log log10 log2 mod multiply nextafter pi
	pow power radians round round_even roundbankers sign signbit sin sinh
	sqrt subtract tan tanh trunc xor`,

	// Strings, regular expressions, encodings and hashes.
	`ascii bar base64 bin bit_length bit_position bitstring character_length
	char_length chr concat concat_ws contains damerau_levenshtein decode
	editdist3 encode ends_with format format_bytes formatreadabledecimalsize
	formatreadablesize from_base64 from_binary from_hex get_bit hamming hash
	hex ilike_escape instr jaccard jaro_similarity jaro_winkler_similarity
	lcase left left_grapheme len length length_grapheme levenshtein
	like_escape lower lpad ltrim md5 md5_number md5_number_lower
	md5_number_upper mismatches nfc_normalize not_ilike_escape
	not_like_escape octet_length ord parse_dirname parse_dirpath
	parse_filename parse_formatted_bytes parse_path position prefix printf
	regexp_escape regexp_extract regexp_extract_all regexp_full_match
	regexp_matches regexp_replace regexp_split_to_array regexp_split_to_table
	repeat replace reverse right right_grapheme rpad rtrim set_bit sha1 sha256
	split split_part starts_with string_split string_split_regex
	string_to_array strip_accents strlen strpos str_split str_split_regex
	substr substring substring_grapheme suffix to_base to_base64 to_binary
	to_hex translate trim ucase unbin unhex unicode upper url_decode
	url_encode`,

	// Date and time, including the clock.
	`age ago century current_date current_localtime current_localtimestamp
	date_add date_diff datediff date_part datepart date_sub datesub date_trunc
	datetrunc day dayname dayofmonth dayofweek dayofyear days_in_month decade
	epoch epoch_ms epoch_ns epoch_us era get_current_time
	get_current_timestamp hour isodow isoyear julian last_day make_date
	make_time make_timestamp make_timestamp_ms make_timestamp_ns
	make_timestamptz microsecond millennium millisecond minute month
	monthname nanosecond normalized_interval now quarter second strftime
	strptime time_bucket timezone timezone_hour timezone_minute to_centuries
	today to_days to_decades to_hours to_microseconds to_millennia
	to_milliseconds to_minutes to_months to_quarters to_seconds to_timestamp
	to_weeks to_years transaction_timestamp try_strptime week weekday
	weekofyear year yearweek`,

	// Lists and arrays.
	`apply array_append array_apply
	array_cat array_concat array_contains array_cosine_distance
	array_cosine_similarity array_cross_product array_distance array_distinct
	array_dot_product array_extract array_filter array_grade_up array_has
	array_has_all array_has_any array_indexof array_inner_product
	array_intersect array_length array_negative_dot_product
	array_negative_inner_product array_pop_back array_pop_front array_position
	array_prepend array_push_back array_push_front array_reduce array_resize
	array_reverse array_reverse_sort array_select array_slice array_sort
	array_to_string array_transform array_unique array_value array_where
	array_zip cardinality element_at filter flatten generate_series
	generate_subscripts grade_up list_any_value
	list_append list_apply list_approx_count_distinct list_avg list_bit_and
	list_bit_or list_bit_xor list_bool_and list_bool_or list_cat list_concat
	list_contains list_cosine_distance list_cosine_similarity list_count
	list_distance list_distinct list_dot_product list_element list_entropy
	list_extract list_filter list_first list_grade_up list_has list_has_all
	list_has_any list_histogram list_indexof list_inner_product list_intersect
	list_kurtosis list_kurtosis_pop list_last list_mad list_max list_median
	list_min list_mode list_negative_dot_product list_negative_inner_product
	list_pack list_position list_prepend list_product list_reduce list_resize
	list_reverse list_reverse_sort list_select list_sem list_skewness
	list_slice list_sort list_stddev_pop list_stddev_samp list_string_agg
	list_sum list_transform list_unique list_value list_var_pop list_var_samp
	list_where list_zip range reduce`,

	// Structs, maps, unions, enums and JSON.
	`array_to_json enum_code from_json from_json_strict json json_array
	json_array_length json_contains json_exists json_extract json_extract_path
	json_extract_path_text json_extract_string json_group_array
	json_group_object json_group_structure json_keys json_merge_patch
	json_object json_pretty json_quote json_structure json_transform
	json_transform_strict json_type json_valid json_value map map_concat
	map_contains map_contains_entry map_contains_value map_entries map_extract
	map_extract_value map_from_entries map_keys map_values row row_to_json
	struct_concat struct_contains struct_extract struct_extract_at struct_has
	struct_indexof struct_insert struct_keys struct_pack struct_position
	struct_update struct_values to_json union_extract union_tag union_value`,

	// Conditionals, random values and uuids.
	`gen_random_uuid nullif random uuid uuid_extract_timestamp
	uuid_extract_version uuidv4 uuidv7`,
)

func nameSet(groups ...string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, g := range groups {
		for _, name := range strings.Fields(g) {
			set[name] = struct{}{}
		}
	}
	return set
}
