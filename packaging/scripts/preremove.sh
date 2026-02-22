#!/bin/bash
set -e

systemctl disable --now coredns-ztnet.service || true
