package service

// CostAlgorithmVersion covers the externally visible cost semantics. Version
// 1.7.0 keeps Spark shadow accounts from re-charging the parent subscription,
// aligns backend cost-profile normalization with the frontend, isolates
// malformed stored profiles, counts paid cycles for deleted recurring
// purchases, marks fallback exchange rates as partial and adopts economics
// projection 1.2.0. It does not change Sub2API token prices.
const CostAlgorithmVersion = "1.7.0"
