set -uo pipefail
export LC_ALL=C
dir="$1"; ip="$2"; cn="$3"
# $2 is the address of the server (a VM) or the name of its Service (a pod): the certificate is for what the clients connect to
if [[ "$ip" =~ ^[0-9.]+$ ]]; then san="IP:$ip"; seen="IP Address:$ip"; else san="DNS:$ip"; seen="DNS:$ip"; fi
umask 077
mkdir -p "$dir" && chmod 700 "$dir" || { echo "nats cert: cannot create $dir"; exit 1; }
crt="$dir/server.crt"; key="$dir/server.key"
if [ -s "$crt" ] && [ -s "$key" ]; then
  # no renewal: groups are short-lived. A certificate that is not usable any more ends the group.
  if ! openssl x509 -in "$crt" -noout -checkend 0 >/dev/null 2>&1; then
    echo "nats cert: the certificate has expired; create the group again"; exit 1
  fi
  if ! openssl x509 -in "$crt" -noout -ext subjectAltName 2>/dev/null | grep -qF "$seen"; then
    echo "nats cert: the certificate is not for $ip (the address of the server changed); create the group again"; exit 1
  fi
  echo "nats cert: kept ($(openssl x509 -in "$crt" -noout -enddate | cut -d= -f2))"; exit 0
fi
# a leaf certificate, not a CA: openssl req -x509 marks the certificate CA:TRUE by default, and rustls
# (every Rust client, e.g. Nuntius) refuses such a certificate for a server (CaUsedAsEndEntity); Go clients do not
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -keyout "$key.new" -out "$crt.new" \
  -days 31 -subj "/CN=$cn" \
  -addext "basicConstraints=critical,CA:FALSE" -addext "keyUsage=critical,digitalSignature" \
  -addext "extendedKeyUsage=serverAuth" -addext "subjectAltName=$san" >/dev/null 2>&1 || { rm -f "$key.new" "$crt.new"; echo "nats cert: openssl failed"; exit 1; }
chmod 600 "$key.new"; chmod 644 "$crt.new"
mv "$key.new" "$key" && mv "$crt.new" "$crt" || { echo "nats cert: cannot write the files"; exit 1; }
echo "nats cert: created (valid 31 days, for $ip)"
