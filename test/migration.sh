set -e

echo $(date) start >> migration.log

{{.DefaultScript}}

echo $(date) done >> migration.log
