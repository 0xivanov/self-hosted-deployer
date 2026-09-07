#!/bin/sh
set -eu

/usr/bin/systemctl start deployer-postgres-globals-backup.service
/usr/bin/cat /var/backups/money-manager/postgres-globals.sql
