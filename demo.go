package main

// demoToggle não tem efeito sobre o serviço. Existe para exercitar o fluxo
// commit -> CI -> imagem no ECR -> CD -> Argo CD sincronizando. Incremente o
// valor e abra um PR. Remova este arquivo quando a demonstração terminar.
const demoToggle = "1"
