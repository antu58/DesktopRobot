#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:9011}"
SESSION_ID="${SESSION_ID:-bench-$(date +%s)}"
ROUNDS="${ROUNDS:-10}"

if [[ "${ROUNDS}" -lt 1 ]]; then
  echo "ROUNDS must be >= 1" >&2
  exit 1
fi

prompts=(
  "你好，今天北京天气大概怎么样？"
  "如果今天有小雨，我出门需要带什么？"
  "帮我安排一个今晚7点到9点的学习计划。"
  "明天早上8点提醒我开晨会，怎么表达更简洁？"
  "我中午12点半有个午饭会，给我一个不迟到建议。"
  "如果下午要写周报，建议我几点开始比较合适？"
  "今天温度偏低的话，运动安排要怎么调整？"
  "帮我把明天的三个重点任务按优先级排一下。"
  "如果晚上要早睡，晚饭和洗漱时间怎么安排？"
  "最后帮我总结今天和明天的日程重点。"
)

if [[ "${#prompts[@]}" -lt "${ROUNDS}" ]]; then
  echo "Built-in prompts are fewer than ROUNDS=${ROUNDS}" >&2
  exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

declare -a times_ms

printf 'session_id=%s\n' "${SESSION_ID}"
printf 'base_url=%s\n' "${BASE_URL}"
printf 'round,time_ms,http_code,message\n'

for ((i=0; i<ROUNDS; i++)); do
  round=$((i + 1))
  msg="${prompts[$i]}"
  safe_msg="$(printf '%s' "${msg}" | sed 's/"/\\"/g')"
  payload="{\"session_id\":\"${SESSION_ID}\",\"message\":\"${safe_msg}\"}"
  resp_file="${tmp_dir}/round_${round}.json"

  meta="$(
    curl -sS \
      -o "${resp_file}" \
      -w '%{time_total} %{http_code}' \
      -X POST "${BASE_URL}/ask" \
      -H 'content-type: application/json' \
      --data "${payload}"
  )"

  time_s="$(printf '%s' "${meta}" | awk '{print $1}')"
  http_code="$(printf '%s' "${meta}" | awk '{print $2}')"
  time_ms="$(awk "BEGIN{printf \"%.2f\", ${time_s}*1000}")"
  times_ms+=("${time_ms}")

  printf '%d,%s,%s,"%s"\n' "${round}" "${time_ms}" "${http_code}" "${msg}"
done

sorted_file="${tmp_dir}/times_sorted.txt"
printf '%s\n' "${times_ms[@]}" | sort -n > "${sorted_file}"
count="$(wc -l < "${sorted_file}" | tr -d '[:space:]')"

avg_ms="$(printf '%s\n' "${times_ms[@]}" | awk '{sum+=$1} END { if (NR==0) print "0.00"; else printf "%.2f", sum/NR }')"
p50_idx="$(( (count + 1) / 2 ))"
p95_idx="$(( (count * 95 + 99) / 100 ))"
if [[ "${p95_idx}" -lt 1 ]]; then p95_idx=1; fi
if [[ "${p95_idx}" -gt "${count}" ]]; then p95_idx="${count}"; fi

p50_ms="$(sed -n "${p50_idx}p" "${sorted_file}")"
p95_ms="$(sed -n "${p95_idx}p" "${sorted_file}")"
min_ms="$(head -n 1 "${sorted_file}")"
max_ms="$(tail -n 1 "${sorted_file}")"

printf 'summary,avg_ms=%s,p50_ms=%s,p95_ms=%s,min_ms=%s,max_ms=%s\n' \
  "${avg_ms}" "${p50_ms}" "${p95_ms}" "${min_ms}" "${max_ms}"
