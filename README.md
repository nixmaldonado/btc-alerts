# Event-Driven Architecture Implementation

"btc-alerts" is a subscription service to get alerts on Bitcoin price.

The main purpose of this project is to showcase an event-driven architecture implementation.

2 modes of alerts available:
- Rolling 24 hours % change threshold
- Price target reached (fired once after armed)

### DoW attacks 

The service is protected against Denial-of-Wallet attacks through three layers: 
- API Gateway usage plans with per-key quotas
- API Gateway request throttling
- Lambda reserved concurrency. 
 
This caps the realistic cost impact of API abuse at a few dollars even under sustained attack.