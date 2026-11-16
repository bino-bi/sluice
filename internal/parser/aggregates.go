// SPDX-License-Identifier: AGPL-3.0-or-later

package parser

// aggregateFunctions are the names duckdb_functions() of the embedded
// DuckDB lists with function_type 'aggregate'. TestAggregateFunctionsMatchDuckDB
// in internal/executor keeps the list in step with DuckDB.
var aggregateFunctions = nameSet(`any_value approx_count_distinct
	approx_quantile approx_top_k arbitrary arg_max arg_max_null
	arg_max_nulls_last arg_min arg_min_null arg_min_nulls_last argmax argmin
	array_agg avg bit_and bit_or bit_xor bitstring_agg bool_and bool_or
	combine_aggr corr count count_if count_star countif covar_pop covar_samp
	decimal_average entropy favg first fsum group_concat histogram
	histogram_exact kahan_sum kurtosis kurtosis_pop last list listagg lttb mad
	max max_by mean median min min_by mode product quantile quantile_cont
	quantile_disc regr_avgx regr_avgy regr_count regr_intercept regr_r2
	regr_slope regr_sxx regr_sxy regr_syy reservoir_quantile sem skewness
	stddev stddev_pop stddev_samp string_agg sum sum_no_overflow sumkahan
	var_pop var_samp variance variant_group_object`)

// aggregateMacros are DuckDB macros over an aggregate.
var aggregateMacros = nameSet(`geomean geometric_mean wavg weighted_avg`)

// IsAggregateFunction reports whether name, folded to lower case, is a
// DuckDB aggregate function or a macro over one.
func IsAggregateFunction(name string) bool {
	name = FoldASCII(name)
	_, agg := aggregateFunctions[name]
	_, macro := aggregateMacros[name]
	return agg || macro
}

// AggregateFunctions returns the names of aggregateFunctions, sorted.
func AggregateFunctions() []string { return sortedKeys(aggregateFunctions) }

// AggregateMacros returns the names of aggregateMacros, sorted.
func AggregateMacros() []string { return sortedKeys(aggregateMacros) }
