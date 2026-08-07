#!/usr/bin/env bash

set -u
set -o pipefail

COUNT=${COUNT:-10}
TOPN=${TOPN:-30}
SLEEP_META=${SLEEP_META:-0.15}
META_CACHE_DIR="${META_CACHE_DIR:-/tmp/dns_meta_cache}"
mkdir -p "$META_CACHE_DIR" >/dev/null 2>&1 || true

# deps
require_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing dependency: $1"; exit 1; }; }
require_cmd curl
require_cmd awk
require_cmd sed
require_cmd tr
require_cmd grep
require_cmd ping

PING_BIN="ping"
PING6_BIN="ping -6"
if command -v ping6 >/dev/null 2>&1; then
  PING6_BIN="ping6"
fi

# color banner (red)
if [ -t 1 ] && [ "${TERM:-}" != "dumb" ]; then
  RED=$'\033[1;31m'
  RESET=$'\033[0m'
else
  RED=""
  RESET=""
fi
echo ""
printf "%b\n\n" "${RED}〔makex-ui 面板〕专属 “服务器 DNS 检测”${RESET}"

# extract valid IPs (IPv4 strict / IPv6 loose)
extract_ips() {
  sed -E 's/<[^>]+>/ /g' \
  | tr -c '0-9A-Fa-f:.' '\n' \
  | grep -E '(^([0-9]{1,3}\.){3}[0-9]{1,3}$)|(^([0-9A-Fa-f]{0,4}:){2,7}[0-9A-Fa-f]{0,4}$)' \
  | awk '!seen[$0]++' \
  | awk -F. '
      $0 ~ /:/ { print; next }
      NF==4 {
        ok=1
        for(i=1;i<=4;i++){
          if($i !~ /^[0-9]+$/ || $i<0 || $i>255){ ok=0; break }
        }
        if(ok) print $0
      }
    '
}

# filter private/reserved IPv4
is_public_ipv4() {
  local ip="$1"; IFS=. read -r a b c d <<<"$ip" || return 1
  if ((a==10)) || ((a==127)) || ((a==192 && b==168)) || ((a==169 && b==254)) \
     || ((a==172 && b>=16 && b<=31)) || ((a==100 && b>=64 && b<=127)) \
     || ((a==0)) || ((a>=224)); then
    return 1
  fi
  return 0
}

# normalize country to slug
normalize_country_to_slug() {
  local raw="$1"
  local x compact
  x=$(printf '%s' "$raw" | tr '[:upper:]' '[:lower:]')
  x=$(printf '%s' "$x" | sed -E 's/^[[:space:]]+|[[:space:]]+$//g')
  compact=$(printf '%s' "$x" | sed -E 's/[^a-z0-9]+//g')
  case "$compact" in
    jp) echo "japan"; return;;
    kr|rok) echo "southkorea"; return;;
    us|usa) echo "unitedstates"; return;;
    uk|gb) echo "unitedkingdom"; return;;
    ae|uae) echo "unitedarabemirates"; return;;
    hk) echo "hongkong"; return;;
    tw) echo "taiwan"; return;;
    cn) echo "china"; return;;
    de) echo "germany"; return;;
    fr) echo "france"; return;;
    it) echo "italy"; return;;
    es) echo "spain"; return;;
    ru) echo "russia"; return;;
    sg) echo "singapore"; return;;
    th) echo "thailand"; return;;
    vn) echo "vietnam"; return;;
    ph) echo "philippines"; return;;
    id) echo "indonesia"; return;;
    my) echo "malaysia"; return;;
    au) echo "australia"; return;;
    nz) echo "newzealand"; return;;
    nl) echo "netherlands"; return;;
    be) echo "belgium"; return;;
    pl) echo "poland"; return;;
    cz) echo "czechia"; return;;
    ch) echo "switzerland"; return;;
    at) echo "austria"; return;;
    se) echo "sweden"; return;;
    no) echo "norway"; return;;
    fi) echo "finland"; return;;
    pt) echo "portugal"; return;;
    ro) echo "romania"; return;;
    hu) echo "hungary"; return;;
    sk) echo "slovakia"; return;;
    si) echo "slovenia"; return;;
    gr) echo "greece"; return;;
    ie) echo "ireland"; return;;
    mx) echo "mexico"; return;;
    ca) echo "canada"; return;;
    br) echo "brazil"; return;;
    ar) echo "argentina"; return;;
    cl) echo "chile"; return;;
    za) echo "southafrica"; return;;
  esac
  x=$(printf '%s' "$x" | sed -E 's/[[:space:]]+//g')
  echo "$x"
}

# fetch IPs from publicdnsserver page
fetch_country_ips() {
  local slug="$1"
  local url="https://publicdnsserver.com/${slug}/"
  local html
  html=$(curl -sL --max-time 15 "$url" || true)
  [ -z "$html" ] && { echo ""; return 0; }
  printf '%s' "$html" | extract_ips
}

# metadata (ip-api) with 1h cache
get_meta_json() {
  local ip="$1"
  local cache="$META_CACHE_DIR/$ip.json"
  local epoch mtime age
  if [ -s "$cache" ]; then
    epoch=$(date +%s)
    if mtime=$(stat -c %Y "$cache" 2>/dev/null); then
      age=$((epoch - mtime))
      if [ "$age" -lt 3600 ]; then
        cat "$cache"; return 0
      fi
    fi
  fi
  local resp
  resp=$(curl -s --max-time 5 "http://ip-api.com/json/$ip?fields=status,message,country,city,as,asname,org,isp,query")
  if [ -n "$resp" ]; then
    printf '%s' "$resp" >"$cache" 2>/dev/null || true
    printf '%s' "$resp"
  else
    printf '{"status":"fail","country":"","city":"","as":"","asname":"","org":"","isp":"","query":"%s"}' "$ip"
  fi
  sleep "$SLEEP_META"
}

# poor-man's json getter (no jq)
json_get() {
  echo "$1" | sed -n "s/.*\"$2\":\"\([^\"]*\)\".*/\1/p"
}

# parse ping output (returns: loss,min,avg,max,mdev)
parse_ping() {
  local out; out=$(cat)
  local loss min avg max mdev rtt
  loss=$(printf '%s\n' "$out" | LC_ALL=C grep -Eo '[0-9]+(\.[0-9]+)?% packet loss' | sed -E 's/%.*//')
  [ -z "$loss" ] && loss="100.0"
  rtt=$(printf '%s\n' "$out" | LC_ALL=C grep -E 'min/avg/max' | tail -n1 | awk -F'=' '{print $2}' | awk '{print $1}')
  if [ -n "$rtt" ]; then
    IFS=/ read -r min avg max mdev <<<"$rtt"
  else
    min="N/A"; avg="N/A"; max="N/A"; mdev="N/A"
  fi
  printf '%s,%s,%s,%s,%s\n' "$loss" "$min" "$avg" "$max" "$mdev"
}

# main
read -rp "请输入英文国家名（如 Japan / United States / South Korea；或 ISO 两字母，如 JP）： " USER_REGION || USER_REGION=""
SLUG=$(normalize_country_to_slug "${USER_REGION:-}")
[ -z "$SLUG" ] && SLUG="japan"

MAP_IPS_RAW=$(fetch_country_ips "$SLUG")

declare -a TARGETS TMP_LIST RESULTS
declare -A seen

while IFS= read -r ip; do
  [ -z "$ip" ] && continue
  if [[ "$ip" == *:* ]]; then
    TARGETS+=("$ip")
  else
    if is_public_ipv4 "$ip"; then
      TARGETS+=("$ip")
    fi
  fi
done < <(printf '%s\n' "$MAP_IPS_RAW" | awk 'NF{if(!seen[$0]++){print}}')

for ip in "${TARGETS[@]}"; do
  if [ -z "${seen[$ip]+x}" ]; then
    TMP_LIST+=("$ip"); seen["$ip"]=1
  fi
  [ "${#TMP_LIST[@]}" -ge "$TOPN" ] && break
done
TARGETS=("${TMP_LIST[@]}")

for must in 1.1.1.1 8.8.8.8; do
  if [ -z "${seen[$must]+x}" ]; then
    TARGETS+=("$must"); seen["$must"]=1
  fi
done

N=${#TARGETS[@]}
printf "地区: %s（slug: %s）| 目标数: %d | 每个目标 ping 次数: %d\n" "${USER_REGION:-N/A}" "$SLUG" "$N" "$COUNT"
printf "将测试的目标：%s\n" "$(printf '%s ' "${TARGETS[@]}")"
echo "开始测试 ......"

if [ "$N" -eq 0 ]; then
  echo "未找到可用目标。"
  exit 0
fi

idx=0
for ip in "${TARGETS[@]}"; do
  idx=$((idx+1))
  pct=$(( idx * 100 / (N==0 ? 1 : N) ))

  printf "进度: [%d/%d | %3d%%] #%d   正在测试: %s\r" "$idx" "$N" "$pct" "$idx" "$ip"

  if [[ "$ip" == *:* ]]; then
    out=$(LC_ALL=C $PING6_BIN -n -c "$COUNT" -i 0.2 -w $((COUNT+4)) "$ip" 2>&1 || true)
  else
    out=$(LC_ALL=C $PING_BIN  -n -c "$COUNT" -i 0.2 -w $((COUNT+4)) "$ip" 2>&1 || true)
  fi

  stats=$(printf '%s' "$out" | parse_ping)
  IFS=, read -r loss min avg max mdev <<<"$stats"

  meta=$(get_meta_json "$ip")
  country=$(json_get "$meta" country); [ -z "$country" ] && country="N/A"
  city=$(json_get "$meta" city);       [ -z "$city" ] && city="N/A"
  asfull=$(json_get "$meta" as);       [ -z "$asfull" ] && asfull="N/A"
  asname=$(json_get "$meta" asname);   [ -z "$asname" ] && asname="N/A"
  org=$(json_get "$meta" org)
  isp=$(json_get "$meta" isp)

  asn=$(printf '%s' "$asfull" | sed -n 's/.*\(AS[0-9][0-9]*\).*/\1/p')
  [ -z "$asn" ] && asn="N/A"

  company="$asname"
  [ -z "$company" ] || [ "$company" = "N/A" ] && company="$org"
  [ -z "$company" ] || [ "$company" = "N/A" ] && company="$isp"
  [ -z "$company" ] && company="N/A"

  printf "进度: [%d/%d | %3d%%] #%d   正在测试: %-39s | 丢包 %s%% | 最小 %sms | 平均 %sms | 最大 %sms | 抖动 %sms\n" \
         "$idx" "$N" "$pct" "$idx" "$ip" "$loss" "$min" "$avg" "$max" "$mdev"

  sortkey="$avg"; [[ "$sortkey" == "N/A" || -z "$sortkey" ]] && sortkey=999999999
  RESULTS+=("$sortkey\t$idx\t$ip\t$country/$city\t$asn\t$company\t$loss\t$min\t$avg\t$max\t$mdev")
done
echo

HEADER=$'编号\t目标\t地区\tASN\t公司\t丢包\t最小(ms)\t平均(ms)\t最大(ms)\t抖动'
BODY=$(
  printf '%b\n' "${RESULTS[@]}" \
  | LC_ALL=C sort -t$'\t' -k1,1n \
  | cut -f2- \
  | while IFS=$'\t' read -r idx0 ip region asn company loss min avg max mdev; do
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$idx0" "$ip" "$region" "$asn" "$company" \
        "$(printf '%.1f%%' "${loss:-0}")" \
        "${min:-N/A}" "${avg:-N/A}" "${max:-N/A}" "${mdev:-N/A}"
    done
)

if command -v column >/dev/null 2>&1; then
  { printf '%s\n' "$HEADER"; printf '%s\n' "$BODY"; } | column -t -s $'\t'
else
  printf "%-4s %-39s %-18s %-8s %-28s %-6s %-9s %-9s %-9s %-7s\n" \
    "编号" "目标" "地区" "ASN" "公司" "丢包" "最小(ms)" "平均(ms)" "最大(ms)" "抖动"
  printf -- "-----------------------------------------------------------------------------------------------\n"
  printf '%s\n' "$BODY" | while IFS=$'\t' read -r idx0 ip region asn company loss min avg max mdev; do
    printf "%-4s %-39s %-18s %-8s %-28s %-6s %-9s %-9s %-9s %-7s\n" \
      "$idx0" "$ip" "$region" "$asn" "$company" \
      "$loss" "$min" "$avg" "$max" "$mdev"
  done

fi

