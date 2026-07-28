# ccyproxy launcher contract, version ccyfix.4.
# Source this file from zsh after installing it with install.sh.

typeset -g CCYPROXY_LAUNCHER_VERSION='ccyfix.4'
unalias ccyproxy 2>/dev/null

_ccyproxy_unset_provider_env() {
  env -u ANTHROPIC_BASE_URL \
    -u ANTHROPIC_AUTH_TOKEN \
    -u ANTHROPIC_API_KEY \
    -u ANTHROPIC_MODEL \
    -u ANTHROPIC_DEFAULT_OPUS_MODEL \
    -u ANTHROPIC_DEFAULT_SONNET_MODEL \
    -u ANTHROPIC_DEFAULT_HAIKU_MODEL \
    -u ANTHROPIC_SMALL_FAST_MODEL \
    -u ANTHROPIC_SMALL_BASE_URL \
    "$@"
}

# Claude Code through the live CLIProxyAPI model catalogue.
# Usage: ccyproxy [--proxy-model MODEL|--proxy-route ROUTE] [--proxy-haiku-model MODEL]
#                 [--list-models|--list-routes|--list-all|--show-config] [claude arguments...]
function ccyproxy {
  local proxy_config="${CCYPROXY_CONFIG:-${HOME}/Library/Application Support/AIUsage/CLIProxyAPI/config.yaml}"
  local proxy_host proxy_port proxy_key proxy_base proxy_models proxy_catalog
  local proxy_routes proxy_route_config proxy_model proxy_requested_model proxy_haiku_model
  local proxy_context_window proxy_max_output_tokens proxy_translation_margin proxy_provider_compact_limit
  local proxy_output_allowance proxy_summary_reserve proxy_compact_trigger proxy_auto_compact_window
  local proxy_capacity_complete proxy_capacity_source proxy_capacity_blockers proxy_availability_error
  local proxy_show_config=0
  local -a proxy_claude_args

  if [[ ! -r "$proxy_config" ]]; then
    echo "ccyproxy: CLIProxyAPI config is not readable: $proxy_config" >&2
    return 1
  fi

  local -a proxy_connection
  proxy_connection=("${(@f)$(ruby -ryaml -e '
    c = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
    puts(c["host"].to_s.empty? || c["host"] == "0.0.0.0" ? "127.0.0.1" : c["host"])
    puts(c["port"] || 8317)
    puts(Array(c["api-keys"]).first.to_s)
  ' "$proxy_config" 2>/dev/null)}")
  proxy_host="${proxy_connection[1]:-127.0.0.1}"
  proxy_port="${proxy_connection[2]:-8317}"
  proxy_key="${proxy_connection[3]:-}"
  proxy_base="http://${proxy_host}:${proxy_port}"
  proxy_routes="$(ruby -ryaml -rjson -e '
    c = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
    puts JSON.generate((c["smart-aliases"] || {}).keys.sort)
  ' "$proxy_config" 2>/dev/null)" || proxy_routes='[]'
  proxy_route_config="$(ruby -ryaml -rjson -e '
    c = YAML.safe_load(File.read(ARGV.fetch(0)), aliases: true)
    puts JSON.generate(c["smart-aliases"] || {})
  ' "$proxy_config" 2>/dev/null)" || proxy_route_config='{}'

  if [[ -z "$proxy_key" ]]; then
    echo "ccyproxy: no client API key found in $proxy_config" >&2
    return 1
  fi

  proxy_models="$(curl -fsS --max-time 10 \
    -H "Authorization: Bearer ${proxy_key}" \
    "${proxy_base}/v1/models" 2>/dev/null)" || {
      echo "ccyproxy: CLIProxyAPI is unavailable at $proxy_base" >&2
      return 1
    }
  proxy_catalog="$(curl -fsS --max-time 10 \
    -H "Authorization: Bearer ${proxy_key}" \
    "${proxy_base}/v1/models?client_version=ccyproxy" 2>/dev/null)" || {
      echo "ccyproxy: CLIProxyAPI model metadata is unavailable at $proxy_base" >&2
      return 1
    }

  case "${1:-}" in
    --list-models)
      printf '%s' "$proxy_catalog" | ruby -rjson -e '
        models = JSON.parse(STDIN.read).fetch("models", [])
        models.sort_by { |m| m["slug"].to_s }.each do |m|
          availability = m["credential_availability"] || {}
          status = availability["status"] || "unknown"
          eligible = availability.key?("eligible_credentials") ? "#{availability["eligible_credentials"]}/#{availability["total_credentials"]}" : "-"
          puts "%-48s %10s %-11s %s" % [m["slug"], m["context_window"] || m["max_context_window"] || "unknown", status, eligible]
        end
      '
      return
      ;;
    --list-routes)
      PROXY_ROUTES="$proxy_routes" ruby -rjson -e '
        puts JSON.parse(ENV.fetch("PROXY_ROUTES"))
      '
      return
      ;;
    --list-all)
      printf 'Routes:\n'
      PROXY_ROUTES="$proxy_routes" ruby -rjson -e '
        puts JSON.parse(ENV.fetch("PROXY_ROUTES")).map { |id| "  #{id}" }
      '
      printf 'Models:\n'
      printf '%s' "$proxy_catalog" | ruby -rjson -e '
        models = JSON.parse(STDIN.read).fetch("models", [])
        models.sort_by { |m| m["slug"].to_s }.each do |m|
          availability = m["credential_availability"] || {}
          status = availability["status"] || "unknown"
          eligible = availability.key?("eligible_credentials") ? "#{availability["eligible_credentials"]}/#{availability["total_credentials"]}" : "-"
          puts "  %-46s %10s %-11s %s" % [m["slug"], m["context_window"] || m["max_context_window"] || "unknown", status, eligible]
        end
      '
      return
      ;;
  esac

  proxy_model="${CCYPROXY_MODEL:-}"
  proxy_haiku_model="${CCYPROXY_HAIKU_MODEL:-}"
  proxy_claude_args=()
  while (( $# > 0 )); do
    case "$1" in
      --proxy-model|--proxy-route)
        if [[ -z "${2:-}" ]]; then
          echo "ccyproxy: ${1} requires a name" >&2
          return 2
        fi
        proxy_model="$2"
        shift 2
        ;;
      --proxy-haiku-model)
        if [[ -z "${2:-}" ]]; then
          echo "ccyproxy: ${1} requires a name" >&2
          return 2
        fi
        proxy_haiku_model="$2"
        shift 2
        ;;
      --show-config)
        proxy_show_config=1
        shift
        ;;
      *)
        proxy_claude_args+=("$1")
        shift
        ;;
    esac
  done
  if [[ -z "$proxy_model" ]]; then
    proxy_model="$(printf '%s' "$proxy_models" | PROXY_ROUTES="$proxy_routes" ruby -rjson -e '
      ids = JSON.parse(STDIN.read).fetch("data", []).map { |m| m["id"] }
      routes = JSON.parse(ENV.fetch("PROXY_ROUTES"))
      preferred = %w[gpt-5.6-sol gpt-5.6 worker claude-opus-4-6-thinking claude-opus-4-6 claude-sonnet-4-6]
      puts(preferred.find { |id| routes.include?(id) || ids.include?(id) } || routes.first || ids.first.to_s)
    ')"
  fi

  proxy_requested_model="$proxy_model"
  proxy_model="$(printf '%s' "$proxy_catalog" |
    PROXY_MODEL="$proxy_model" PROXY_ROUTE_CONFIG="$proxy_route_config" ruby -rjson -e '
      models = JSON.parse(STDIN.read).fetch("models", [])
      by_slug = models.each_with_object({}) { |m, out| out[m["slug"]] = m }
      selected = ENV.fetch("PROXY_MODEL")
      if by_slug[selected]
        puts selected
        exit
      end
      route = JSON.parse(ENV.fetch("PROXY_ROUTE_CONFIG"))[selected] || {}
      candidates = Array(route["candidates"]).select { |id| by_slug[id] }
      eligible = candidates.find do |id|
        availability = by_slug[id]["credential_availability"]
        availability.is_a?(Hash) && availability["availability_complete"] == true &&
          availability["status"] == "available" && availability["eligible_credentials"].to_i > 0
      end
      puts eligible.to_s
    ')"
  if [[ -z "$proxy_model" ]]; then
    echo "ccyproxy: route '$proxy_requested_model' has no currently eligible candidate with complete availability metadata" >&2
    return 2
  fi

  if [[ -z "$proxy_haiku_model" ]]; then
    proxy_haiku_model="$(printf '%s' "$proxy_catalog" | PROXY_MAIN_MODEL="$proxy_model" ruby -rjson -e '
      models = JSON.parse(STDIN.read).fetch("models", [])
      by_slug = models.each_with_object({}) { |m, out| out[m["slug"]] = m }
      preferred = %w[gpt-5.6-luna gpt-5.4-mini gemini-3.1-flash-lite gemini-3-flash]
      selected = preferred.find do |slug|
        model = by_slug[slug]
        next false unless model
        availability = model["credential_availability"]
        availability.is_a?(Hash) && availability["availability_complete"] == true &&
          availability["status"] == "available" && availability["eligible_credentials"].to_i > 0
      end
      puts(selected || ENV.fetch("PROXY_MAIN_MODEL"))
    ')"
  fi

  if ! printf '%s' "$proxy_models" | PROXY_MODELS="${proxy_model},${proxy_haiku_model}" PROXY_ROUTES="$proxy_routes" ruby -rjson -e '
    ids = JSON.parse(STDIN.read).fetch("data", []).map { |m| m["id"] }
    routes = JSON.parse(ENV.fetch("PROXY_ROUTES"))
    requested = ENV.fetch("PROXY_MODELS").split(",")
    exit(requested.all? { |id| (ids + routes).include?(id) } ? 0 : 1)
  '; then
    echo "ccyproxy: one or more requested models/routes are unavailable: main='$proxy_model' haiku='$proxy_haiku_model'" >&2
    echo "ccyproxy: refreshes automatically; inspect current choices with: ccyproxy --list-all" >&2
    return 2
  fi

  proxy_availability_error="$(printf '%s' "$proxy_catalog" |
    PROXY_MODELS="${proxy_model},${proxy_haiku_model}" PROXY_ROUTE_CONFIG="$proxy_route_config" ruby -rjson -rtime -e '
      catalog = JSON.parse(STDIN.read).fetch("models", [])
      by_slug = catalog.each_with_object({}) { |m, out| out[m["slug"]] = m }
      routes = JSON.parse(ENV.fetch("PROXY_ROUTE_CONFIG"))
      resolve_all = lambda do |selected, seen = []|
        return [by_slug[selected]] if by_slug[selected]
        return [] if seen.include?(selected)
        Array((routes[selected] || {})["candidates"]).flat_map do |candidate|
          resolve_all.call(candidate, seen + [selected])
        end
      end
      failures = ENV.fetch("PROXY_MODELS").split(",").uniq.each_with_object([]) do |selected, out|
        candidates = resolve_all.call(selected)
        unless candidates.any?
          out << "#{selected}: not advertised"
          next
        end
        model = candidates.find do |candidate|
          availability = candidate["credential_availability"]
          availability.is_a?(Hash) && availability["availability_complete"] == true &&
            availability["status"] == "available" && availability["eligible_credentials"].to_i > 0
        end
        next if model

        candidate = candidates.first
        availability = candidate["credential_availability"]
        unless availability.is_a?(Hash)
          out << "#{selected}: availability metadata missing"
          next
        end
        unless availability["availability_complete"] == true
          blockers = Array(availability["availability_blockers"])
          suffix = blockers.empty? ? "" : " (#{blockers.join(", ")})"
          out << "#{selected}: availability metadata incomplete#{suffix}"
          next
        end
        detail = "#{selected}: #{availability["status"] || "unavailable"} (eligible #{availability["eligible_credentials"] || 0}/#{availability["total_credentials"] || 0})"
        if availability["cooldown_until"]
          begin
            detail += ", earliest retry #{Time.parse(availability["cooldown_until"]).getlocal.strftime("%Y-%m-%d %H:%M:%S %Z")}"
          rescue ArgumentError
          end
        elsif availability["retry_after_seconds"]
          detail += ", retry after #{availability["retry_after_seconds"]}s"
        end
        out << detail
      end
      if failures.any?
        puts failures.join("; ")
        exit 1
      end
    ' 2>&1)"
  if (( $? != 0 )); then
    echo "ccyproxy: refusing to launch with unavailable credentials: $proxy_availability_error" >&2
    echo "ccyproxy: inspect live eligibility with: ccyproxy --list-models" >&2
    return 2
  fi

  local -a proxy_capacity
  proxy_capacity=("${(@f)$(printf '%s' "$proxy_catalog" |
    PROXY_MODEL="$proxy_model" ruby -rjson -e '
      model = JSON.parse(STDIN.read).fetch("models", []).find { |m| m["slug"] == ENV.fetch("PROXY_MODEL") }
      exit 1 unless model
      puts model["context_window"].to_i
      puts model["max_output_tokens"].to_i
      puts model["translation_margin_tokens"].to_i
      puts model["auto_compact_token_limit"].to_i
      puts model["capacity_complete"] == true ? "true" : "false"
      puts model["capacity_source"].to_s
      puts JSON.generate(Array(model["capacity_blockers"]))
    ' 2>/dev/null)}")
  proxy_context_window="${proxy_capacity[1]:-0}"
  proxy_max_output_tokens="${proxy_capacity[2]:-0}"
  proxy_translation_margin="${proxy_capacity[3]:-0}"
  proxy_provider_compact_limit="${proxy_capacity[4]:-0}"
  proxy_capacity_complete="${proxy_capacity[5]:-false}"
  proxy_capacity_source="${proxy_capacity[6]:-unknown}"
  proxy_capacity_blockers="${proxy_capacity[7]:-[]}"
  if [[ "$proxy_capacity_complete" != true ]] || (( proxy_context_window <= 0 || proxy_max_output_tokens <= 0 || proxy_translation_margin <= 0 )); then
    echo "ccyproxy: incomplete capacity metadata for main='$proxy_model': $proxy_capacity_blockers" >&2
    return 2
  fi

  proxy_output_allowance="${CLAUDE_CODE_MAX_OUTPUT_TOKENS:-32000}"
  if [[ "$proxy_output_allowance" != <-> ]] || (( proxy_output_allowance <= 0 )); then
    proxy_output_allowance=32000
  fi
  if (( proxy_output_allowance > proxy_max_output_tokens )); then
    proxy_output_allowance="$proxy_max_output_tokens"
  fi
  proxy_compact_trigger=$(( proxy_context_window - proxy_output_allowance - proxy_translation_margin ))
  if (( proxy_provider_compact_limit > 0 && proxy_provider_compact_limit < proxy_compact_trigger )); then
    proxy_compact_trigger="$proxy_provider_compact_limit"
  fi
  if (( proxy_compact_trigger <= 0 )); then
    echo "ccyproxy: invalid compact trigger for main='$proxy_model' (context=$proxy_context_window output=$proxy_output_allowance margin=$proxy_translation_margin)" >&2
    return 2
  fi
  proxy_summary_reserve="$proxy_output_allowance"
  if (( proxy_summary_reserve > 20000 )); then
    proxy_summary_reserve=20000
  fi
  proxy_auto_compact_window=$(( proxy_compact_trigger + proxy_summary_reserve + 13000 ))
  if (( proxy_auto_compact_window > proxy_context_window )); then
    proxy_auto_compact_window="$proxy_context_window"
  fi

  if (( proxy_show_config )); then
    printf 'base_url=%s\n' "$proxy_base"
    if [[ "$proxy_requested_model" != "$proxy_model" ]]; then
      printf 'requested_route=%s\n' "$proxy_requested_model"
      printf 'resolved_candidate=%s\n' "$proxy_model"
    fi
    printf 'main_model=%s\n' "$proxy_model"
    printf 'opus_model=%s\n' "$proxy_model"
    printf 'sonnet_model=%s\n' "$proxy_model"
    printf 'haiku_model=%s\n' "$proxy_haiku_model"
    printf 'subagent_model=%s\n' "$proxy_model"
    printf 'context_window=%s\n' "$proxy_context_window"
    printf 'max_output_tokens=%s\n' "$proxy_max_output_tokens"
    printf 'output_allowance=%s\n' "$proxy_output_allowance"
    printf 'summary_reserve=%s\n' "$proxy_summary_reserve"
    printf 'translation_margin_tokens=%s\n' "$proxy_translation_margin"
    printf 'provider_auto_compact_token_limit=%s\n' "$proxy_provider_compact_limit"
    printf 'compact_trigger=%s\n' "$proxy_compact_trigger"
    printf 'auto_compact_window=%s\n' "$proxy_auto_compact_window"
    printf 'capacity_source=%s\n' "$proxy_capacity_source"
    printf 'effort=medium\n'
    return
  fi

  _ccyproxy_unset_provider_env env \
    ANTHROPIC_AUTH_TOKEN="$proxy_key" \
    ANTHROPIC_API_KEY="$proxy_key" \
    ANTHROPIC_BASE_URL="$proxy_base" \
    ANTHROPIC_MODEL="$proxy_model" \
    ANTHROPIC_DEFAULT_OPUS_MODEL="$proxy_model" \
    ANTHROPIC_DEFAULT_SONNET_MODEL="$proxy_model" \
    ANTHROPIC_DEFAULT_HAIKU_MODEL="$proxy_haiku_model" \
    ANTHROPIC_SMALL_FAST_MODEL="$proxy_haiku_model" \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    CLAUDE_CODE_MAX_CONTEXT_TOKENS="$proxy_context_window" \
    CLAUDE_CODE_MAX_OUTPUT_TOKENS="$proxy_output_allowance" \
    CLAUDE_CODE_AUTO_COMPACT_WINDOW="$proxy_auto_compact_window" \
    CLAUDE_CODE_ALWAYS_ENABLE_EFFORT=1 \
    CLAUDE_CODE_SUBAGENT_MODEL="$proxy_model" \
    CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=0 \
    CLAUDE_LAUNCHER=ccyproxy \
    claude --setting-sources user,project,local --model "$proxy_model" --effort medium \
      --dangerously-skip-permissions --permission-mode bypassPermissions "${proxy_claude_args[@]}"
}
