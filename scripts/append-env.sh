#!/bin/bash

# IP Staging
IP="192.168.88.58"
CVV_SECRET=$(openssl rand -hex 16)

echo "" >> .env
echo "# --- Ditambahkan via script ---" >> .env
echo "FRONTEND_URL=http://$IP" >> .env
echo "VIDA_SIGN_CVV_SECRET_KEY=$CVV_SECRET" >> .env

echo "Variabel FRONTEND_URL dan VIDA_SIGN_CVV_SECRET_KEY berhasil ditambahkan ke .env!"
