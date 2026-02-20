#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SOUL_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

if [[ -f "${SOUL_DIR}/.env" ]]; then
  set -a
  # shellcheck disable=SC1091
  source "${SOUL_DIR}/.env"
  set +a
fi

BASE_URL="${OPENAI_BASE_URL:-}"
API_KEY="${OPENAI_API_KEY:-}"
MODEL="${MODEL:-${LLM_MODEL:-gpt-4o-mini}}"
ROUNDS="${ROUNDS:-3}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-120}"
MAX_TOKENS="${MAX_TOKENS:-64}"
TEMPERATURE="${TEMPERATURE:-0}"
SUITE="${SUITE:-all}" # all|tokens|tools
OUTPUT_CSV="${OUTPUT_CSV:-/tmp/benchmark_llm_factors_$(date +%s).csv}"

TOKEN_WORD_COUNTS="${TOKEN_WORD_COUNTS:-64 256 1024 2048}"
TOOLS_FIXED_WORDS="${TOOLS_FIXED_WORDS:-512}"

if [[ -z "${BASE_URL}" || -z "${API_KEY}" ]]; then
  echo "OPENAI_BASE_URL/OPENAI_API_KEY is required (.env or env)." >&2
  exit 1
fi

if [[ "${ROUNDS}" -lt 1 ]]; then
  echo "ROUNDS must be >= 1" >&2
  exit 1
fi

BASE_URL="${BASE_URL%/}"

gen_text_by_words() {
  local word_count="$1"
  awk -v n="${word_count}" 'BEGIN { for (i=1; i<=n; i++) printf "token%d ", i }'
}

build_tools_json() {
  local mode="$1"
  case "${mode}" in
    none)
      echo "[]"
      ;;
    two)
      jq -nc '[
        {
          type: "function",
          function: {
            name: "light_green",
            description: "认可时点亮绿灯",
            parameters: {type: "object", properties: {}, required: []}
          }
        },
        {
          type: "function",
          function: {
            name: "light_red",
            description: "不认可时点亮红灯",
            parameters: {type: "object", properties: {}, required: []}
          }
        }
      ]'
      ;;
    twenty)
      jq -nc '
        ([
          {
            type: "function",
            function: {
              name: "light_green",
              description: "认可时点亮绿灯",
              parameters: {type: "object", properties: {}, required: []}
            }
          },
          {
            type: "function",
            function: {
              name: "light_red",
              description: "不认可时点亮红灯",
              parameters: {type: "object", properties: {}, required: []}
            }
          }
        ] + [
          range(1; 19) | {
            type: "function",
            function: {
              name: ("dummy_skill_" + (.|tostring)),
              description: "dummy skill for benchmark",
              parameters: {
                type: "object",
                properties: {
                  value: {type: "string"}
                },
                required: []
              }
            }
          }
        ])
      '
      ;;
    *)
      echo "unsupported tool mode: ${mode}" >&2
      exit 1
      ;;
  esac
}

run_case() {
  local case_name="$1"
  local word_count="$2"
  local tools_mode="$3"
  local must_call_tool="$4" # 0|1

  local tools_json
  tools_json="$(build_tools_json "${tools_mode}")"
  local user_text
  user_text="$(gen_text_by_words "${word_count}")"

  if [[ "${must_call_tool}" == "1" ]]; then
    user_text="${user_text} 请必须调用一个工具，不要输出普通文本。"
  else
    user_text="${user_text} 请直接用'收到'回复，不要调用工具。"
  fi

  for ((i=1; i<=ROUNDS; i++)); do
    local payload
    payload="$(
      jq -nc \
        --arg model "${MODEL}" \
        --arg sys "你是延迟测试助手，尽量稳定输出。" \
        --arg user "${user_text}" \
        --argjson max_tokens "${MAX_TOKENS}" \
        --argjson temperature "${TEMPERATURE}" \
        --argjson tools "${tools_json}" \
        '
        {
          model: $model,
          max_tokens: $max_tokens,
          temperature: $temperature,
          messages: [
            {role: "system", content: $sys},
            {role: "user", content: $user}
          ]
        }
        + (if ($tools|length) > 0 then {tools: $tools, tool_choice: "auto"} else {} end)
        '
    )"

    local resp_file meta time_s http_code time_ms
    resp_file="$(mktemp)"
    meta="$(
      curl -sS \
        --max-time "${TIMEOUT_SECONDS}" \
        -o "${resp_file}" \
        -w '%{time_total} %{http_code}' \
        -X POST "${BASE_URL}/chat/completions" \
        -H "Authorization: Bearer ${API_KEY}" \
        -H 'Content-Type: application/json' \
        --data "${payload}"
    )"

    time_s="$(printf '%s' "${meta}" | awk '{print $1}')"
    http_code="$(printf '%s' "${meta}" | awk '{print $2}')"
    time_ms="$(awk "BEGIN { printf \"%.2f\", ${time_s} * 1000 }")"

    local prompt_tokens completion_tokens total_tokens finish_reason tool_calls error_msg
    prompt_tokens="$(jq -r '.usage.prompt_tokens // 0' "${resp_file}")"
    completion_tokens="$(jq -r '.usage.completion_tokens // 0' "${resp_file}")"
    total_tokens="$(jq -r '.usage.total_tokens // 0' "${resp_file}")"
    finish_reason="$(jq -r '.choices[0].finish_reason // ""' "${resp_file}")"
    tool_calls="$(jq -r '[(.choices[0].message.tool_calls // [])[]?.function.name] | join("+")' "${resp_file}")"
    error_msg="$(jq -r '.error.message // ""' "${resp_file}" | tr '\n' ' ' | sed 's/,/;/g')"

    printf '%s,%d,%d,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
      "${case_name}" "${word_count}" "${must_call_tool}" "${tools_mode}" "${i}" \
      "${time_ms}" "${http_code}" "${prompt_tokens}" "${completion_tokens}" "${total_tokens}" \
      "${finish_reason}" "${tool_calls:-}" >> "${OUTPUT_CSV}"

    if [[ "${http_code}" -ge 300 || -n "${error_msg//[[:space:]]/}" ]]; then
      printf '%s,%d,%d,%s,%d,error,%s\n' \
        "${case_name}" "${word_count}" "${must_call_tool}" "${tools_mode}" "${i}" "${error_msg}" >> "${OUTPUT_CSV}.errors"
    fi

    rm -f "${resp_file}"
  done
}

echo "output_csv=${OUTPUT_CSV}"
echo "suite=${SUITE} rounds=${ROUNDS} model=${MODEL} base_url=${BASE_URL}"
echo "case,word_count,must_call_tool,tools_mode,round,time_ms,http_code,prompt_tokens,completion_tokens,total_tokens,finish_reason,tool_calls" > "${OUTPUT_CSV}"

if [[ "${SUITE}" == "all" || "${SUITE}" == "tokens" ]]; then
  for wc in ${TOKEN_WORD_COUNTS}; do
    run_case "token_size_no_tools" "${wc}" "none" "0"
  done
fi

if [[ "${SUITE}" == "all" || "${SUITE}" == "tools" ]]; then
  run_case "tools_none" "${TOOLS_FIXED_WORDS}" "none" "0"
  run_case "tools_2_no_call" "${TOOLS_FIXED_WORDS}" "two" "0"
  run_case "tools_2_must_call" "${TOOLS_FIXED_WORDS}" "two" "1"
  run_case "tools_20_no_call" "${TOOLS_FIXED_WORDS}" "twenty" "0"
  run_case "tools_20_must_call" "${TOOLS_FIXED_WORDS}" "twenty" "1"
fi

echo
echo "[raw_csv]"
sed -n '1,999p' "${OUTPUT_CSV}"

echo
echo "[summary]"
awk -F',' '
NR == 1 { next }
{
  key = $1 "|w=" $2 "|tools=" $4 "|must_call=" $3
  code = $7 + 0
  ms = $6 + 0
  pt = $8 + 0
  tc = $10 + 0
  total[key]++
  if (code >= 200 && code < 300) {
    ok[key]++
    sum_ms[key] += ms
    sum_pt[key] += pt
    sum_tc[key] += tc
  } else {
    err[key]++
  }
}
END {
  printf "case,ok_rounds,total_rounds,error_rounds,avg_time_ms,avg_prompt_tokens,avg_total_tokens\n"
  for (k in total) {
    okn = ok[k] + 0
    avg_ms = (okn > 0) ? (sum_ms[k] / okn) : 0
    avg_pt = (okn > 0) ? (sum_pt[k] / okn) : 0
    avg_tc = (okn > 0) ? (sum_tc[k] / okn) : 0
    printf "%s,%d,%d,%d,%.2f,%.2f,%.2f\n", k, okn, total[k], (err[k]+0), avg_ms, avg_pt, avg_tc
  }
}
' "${OUTPUT_CSV}" | sort

echo
echo "done. csv=${OUTPUT_CSV}"
if [[ -f "${OUTPUT_CSV}.errors" ]]; then
  echo "errors_file=${OUTPUT_CSV}.errors"
fi
